# KEP-5: 基于 ClusterVersion/ReleaseImage/UpgradePath 的声明式集群版本升级

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-5 |
| **标题** | 声明式集群版本管理：基于 ClusterVersion/ReleaseImage/UpgradePath 的 DAG 驱动升级方案 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-05-09 |
| **依赖** | 现有 PhaseFrame 架构、bke-manifests 镜像构建流程、CAPI v1beta1 |

## 1. 摘要

本提案引入 `ClusterVersion`、`ReleaseImage` 与 `UpgradePath` 三个核心 CRD，借鉴 OpenShift CVO 声明式版本管理理念，结合 OCI 镜像分发版本清单，实现 openFuyao 集群的版本升级。方案保持 `BKEClusterReconciler` 为主调度器，通过解析 `ReleaseImage` 构建独立的 **安装 DAG** 与 **升级 DAG**，按拓扑顺序调用 Phase。Phase 升级决策完全复用现有 `NeedExecute()` 、`Execute()`接口，通过注入版本上下文比对当前与目标版本。`bke-manifests` 提供 `ComponentVersion` 清单，支持叶子/组合组件、兼容性约束、依赖拓扑、升级策略及 `inline` 代码引用。架构彻底解耦，支持组件独立演进、平滑迁移与商业化生产级高可用。

## 2. 动机

### 2.1 现状痛点

| 问题 | 现状 | 影响 |
| ------ | ------ | ------ |
| **版本概念缺失** | 版本信息散落在 `BKECluster.Spec` 各字段 | 无法回答“集群当前是什么版本”，升级缺乏统一声明入口 |
| **命令式编排** | Phase 执行顺序硬编码，依赖手动状态判断 | 无法并行、失败难定位、回滚成本高、升级路径固定 |
| **清单分散** | 组件版本与部署文件未集中管理 | 升级时难以追溯版本包含的组件及对应配置 |
| **兼容性盲区** | 组件间版本依赖无集中校验 | 易出现 K8s 与 Etcd/Containerd 版本不兼容导致集群不可用 |
| **耦合度高** | 版本管理与集群生命周期强绑定 | 新增组件或修改升级策略需侵入核心控制器 |

### 2.2 目标

1. 定义 `ClusterVersion`、`ReleaseImage`、`UpgradePath` CRD 及 1:1 关联关系。
2. 设计 `ComponentVersion` 数据结构，支持叶子/组合组件、`inline` 模式、兼容性/依赖约束、升级策略。
3. 实现基于 OCI 的 `ReleaseImage` 与 `UpgradePath` 动态拉取、解析与校验。
4. 在 `BKEClusterReconciler` 中实现独立的安装/升级 DAG 构建与调度引擎。
5. 改造核心 Phase 为 YAML 清单或注册至 `ComponentFactory`，复用 `NeedExecute()` 实现版本比对。
6. 提供从旧版本（无新 CRD）到新版本的平滑迁移方案。

### 2.3 非目标

1. 不替换 CAPI 核心控制器（KCP/MD）的节点生命周期管理。
2. 不修改 `bke-manifests` 现有构建与分发流程。
3. 不在此阶段实现 UI/CLI 交互层或多集群版本同步。
4. 不重写现有 Phase 核心执行逻辑，仅增强触发决策层与上下文注入。

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
| ------ | ------ |
| CRD 定义与注册 | `ClusterVersion`、`ReleaseImage`、`UpgradePath` API 定义 |
| `bke-manifests` 扩展 | 新增 `ComponentVersion` 元数据规范与目录结构 |
| 控制器实现 | 版本声明器、清单验证器、DAG 调度器 |
| 升级路径与兼容性 | 规则引擎、拦截机制、约束求解算法 |
| Phase 适配 | `NeedExecute` 上下文注入、`inline` 映射、`Version()` 接口、ComponentFactory 注册 |

### 3.2 约束

| 约束 | 说明 |
| ------ | ------ |
| **1:1 映射** | 单集群活跃状态下，1 个 `ClusterVersion` 严格对应 1 个 `ReleaseImage` |
| **清单不可变** | `ReleaseImage.Spec` 创建后不可修改 |
| **向后兼容** | 必须支持从现有 `BKECluster` 平滑迁移，Feature Gate 控制开关 |
| **离线环境** | 所有资源通过 OCI/本地缓存提供，支持断网降级 |
| **接口复用** | **严禁新增 `ShouldUpgrade()` 接口**，必须复用 `NeedExecute()` |

## 4. 提案设计

### 4.1 资源属性与关联关系

```txt
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                               │
│  (集群实例，生命周期管理)                                         │
└──────────────────────────┬──────────────────────────────────────┘
                           │ 1:1 (OwnerReference)
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                      ClusterVersion                              │
│  spec.desiredVersion: v2.6.0                                     │
│  status.currentVersion: v2.5.0                                   │
│  status.currentReleaseImageRef: ri-v2.5.0                        │
└──────────────────────────┬───────────────────────────────────────┘
                           │ 1:1 引用
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                       ReleaseImage                               │
│  spec.version: v2.6.0                                            │
│  spec.ociRef: registry/openfuyao-release:v2.6.0                  │
│  spec.install.components: [{name: k8s, ver: v1.29.0}, ...]       │
│  spec.upgrade.components: [{name: provider, ver: v1.2.0}, ...]   │
└──────────────────────────┬───────────────────────────────────────┘
                           │ 按 (name,version) 定位
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│              bke-manifests (ComponentVersion 定义)               │
│  bke-manifests/kubernetes/v1.29.0/component.yaml                 │
│  bke-manifests/provider-upgrade/v1.0.0/component.yaml            │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│                       UpgradePath                                │
│  spec.ociRef: registry/openfuyao-upgradepath:latest              │
│  paths: [{from: v2.5.0, to: v2.6.0, blocked: false}]             │
└──────────────────────────────────────────────────────────────────┘
```

### 4.2 ComponentVersion 数据结构设计

```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: kubernetes-v1.29.0
spec:
  name: kubernetes
  type: composite
  version: v1.29.0
  inline:
    handler: EnsureKubernetesUpgrade
    version: v1.0
  subComponents:
    - name: kube-apiserver
      version: v1.29.0
  compatibility:
    constraints:
      - component: etcd
        rule: ">=3.5.10"
  dependencies:
    - name: etcd
      phase: Upgrade
  upgradeStrategy:
    mode: Batch
    batchSize: 1
    timeout: "30m"
    failurePolicy: FailFast  # FailFast | Continue | Rollback
```

FailurePolicy 设计说明：

- FailFast（默认）：当前组件升级失败，立即终止整个 DAG 执行，标记 UpgradeFailed。
- Continue：跳过失败组件，记录 Warning，继续执行后续无依赖组件。适用于非核心组件。
- Rollback：触发该组件的降级/卸载逻辑，恢复至上一稳定版本后继续 DAG。适用于核心组件。

**Go 结构体定义**：

```go
package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ComponentVersion is the Schema for the componentversions API
type ComponentVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ComponentVersionSpec   `json:"spec,omitempty"`
	Status ComponentVersionStatus `json:"status,omitempty"`
}

// ComponentVersionSpec defines the desired state of ComponentVersion
type ComponentVersionSpec struct {
	Name            string              `json:"name"`
	Type            ComponentType       `json:"type"`
	Version         string              `json:"version"`
	Inline          *InlineSpec         `json:"inline,omitempty"`
	SubComponents   []SubComponent      `json:"subComponents,omitempty"`
	Compatibility   CompatibilitySpec   `json:"compatibility,omitempty"`
	Dependencies    []Dependency        `json:"dependencies,omitempty"`
	UpgradeStrategy UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
	Resources       []ResourceSpec      `json:"resources,omitempty"`
}

// ComponentType defines the type of component installation
type ComponentType string

const (
	ComponentTypeYAML    ComponentType = "yaml"
	ComponentTypeHelm    ComponentType = "helm"
	ComponentTypeInline  ComponentType = "inline"
	ComponentTypeBinary  ComponentType = "binary"
)

// InlineSpec defines the inline handler configuration
type InlineSpec struct {
	Handler string `json:"handler"`
	Version string `json:"version"`
}

// SubComponent defines a sub-component reference
type SubComponent struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// CompatibilitySpec defines compatibility constraints
type CompatibilitySpec struct {
	Constraints []Constraint `json:"constraints,omitempty"`
}

// Constraint defines a single compatibility constraint
type Constraint struct {
	Component string `json:"component"`
	Rule      string `json:"rule"`
}

// Dependency defines a dependency on another component
type Dependency struct {
	Name  string `json:"name"`
	Phase string `json:"phase,omitempty"`
}

// UpgradeStrategySpec defines the upgrade strategy for the component
type UpgradeStrategySpec struct {
	Mode          string `json:"mode,omitempty"`
	BatchSize     int    `json:"batchSize,omitempty"`
	Timeout       string `json:"timeout,omitempty"`
	FailurePolicy string `json:"failurePolicy,omitempty"`
}

// ResourceSpec defines a Kubernetes resource to be applied
type ResourceSpec struct {
	Kind       string            `json:"kind"`
	APIVersion string            `json:"apiVersion"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels,omitempty"`
	Data       map[string]string `json:"data,omitempty"`
	StringData map[string]string `json:"stringData,omitempty"`
	Manifest   string            `json:"manifest,omitempty"`
}

// ComponentVersionStatus defines the observed state of ComponentVersion
type ComponentVersionStatus struct {
	Phase      string             `json:"phase,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

// ComponentVersionList contains a list of ComponentVersion
type ComponentVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComponentVersion `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ComponentVersion{}, &ComponentVersionList{})
}
```

### 4.3 ComponentFactory 与 Phase 重构设计

**ComponentFactory 设计**：

```go
type ComponentFactory struct {
    mu       sync.RWMutex
    registry map[string]ComponentInstance // key: "{name}@{version}"
}

type ComponentInstance struct {
    Name    string
    Version string
    Handler PhaseExecutor // 注册的即为 inline 模式，无需 ExecutionMode
}

func (f *ComponentFactory) Register(name, version string, handler PhaseExecutor) {
    f.mu.Lock()
    defer f.mu.Unlock()
    key := fmt.Sprintf("%s@%s", name, version)
    f.registry[key] = ComponentInstance{Name: name, Version: version, Handler: handler}
}

func (f *ComponentFactory) Resolve(name, version string, inline *InlineSpec) (*ComponentInstance, error) {
    if inline != nil {
        key := fmt.Sprintf("%s@%s", inline.Handler, inline.Version)
        if inst, ok := f.registry[key]; ok {
            return &inst, nil
        }
    }
    return nil, fmt.Errorf("component %s@%s not found", name, version)
}
```

**Phase 重构清单**：

| Phase 名称 | 重构方式 | bke-manifests 映射 | 接口增强 |
| --------- | ------- | ------------- | ----- |
| `EnsureProviderSelfUpgrade` | 转为 YAML 清单 | `provider-upgrade/v1.0.0/component.yaml` | 无 |
| `EnsureAgentUpgrade` | 转为 YAML 清单 | `bkeagent-upgrade/v1.0.0/component.yaml` | 无 |
| `EnsureComponentUpgrade` | 转为 YAML 清单 | `component-upgrade/v1.0.0/component.yaml` | 无 |
| `EnsureEtcdUpgrade` | 转为 YAML 清单 | `etcd-upgrade/v1.0.0/component.yaml` | 无 |
| 其他代码 Phase | 增加 `Version()` 接口，注册至 Factory | 不生成 YAML | `Version() string`, `NeedExecute()` 增强 |

### 4.4 bke-manifests 目录与 OCI 镜像设计

#### 4.4.1 bke-manifests 目录规范

```txt
bke-manifests/
├── kubernetes/
│   ├── v1.28.0/
│   │   └── component.yaml
│   └── v1.29.0/
│       └── component.yaml
├── etcd/
│   └── v3.5.12/
│       └── component.yaml
└── provider-upgrade/
    └── v1.0.0/
        └── component.yaml
```

#### 4.4.2 ReleaseImage OCI 样例 (YAML)

```yaml
version: "v2.6.0"
ociRef: "registry/openfuyao-release:v2.6.0"
install:
  components:
    - name: kubernetes
      version: v1.29.0
    - name: etcd
      version: v3.5.12
upgrade:
  components:
    - name: pre-upgrade-resources
      version: v1.0.0
    - name: provider-upgrade
      version: v1.2.0
```

#### 4.4.3 UpgradePath OCI 样例与单 CR 映射设计

**设计原则**：UpgradePath OCI 镜像对应**单个 CR**，而非多个 CR。所有升级路径定义聚合在一个 `UpgradePath` 资源中，方便用户通过 `kubectl get upgradepath` 统一查看与管理。

**OCI 镜像结构**：

```yaml
# OCI 镜像内的 paths.yaml (对应单个 UpgradePath CR)
apiVersion: cvo.openfuyao.cn/v1beta1
kind: UpgradePath
metadata:
  name: openfuyao-upgrade-paths
  annotations:
    cvo.openfuyao.cn/oci-digest: "sha256:abc123..."
spec:
  paths:
    - from: "v2.4.0"
      to: "v2.5.0"
      blocked: false
      deprecated: false
    - from: "v2.5.0"
      to: "v2.6.0"
      blocked: false
      deprecated: false
    - from: "v2.4.0"
      to: "v2.6.0"
      blocked: true
      deprecated: false
      notes: "Direct upgrade blocked, please upgrade via v2.5.0"
```

**Latest 镜像 Digest 监控设计**：

由于 UpgradePath 使用 `:latest` 标签，需要持续监控镜像 digest 变更以获取最新路径定义。

**监控机制**：

```txt
┌─────────────────────────────────────────────────────────┐
│              UpgradePath Digest Monitor                 │
├─────────────────────────────────────────────────────────┤
│ 1. 定时轮询 (默认 5m)                                    │
│    ├─ 调用 registry HEAD 请求获取当前 digest             │
│    ├─ 对比缓存中的 lastKnownDigest                       │
│    ├─ 相同 → 跳过                                       │
│    └─ 不同 → 触发重新拉取与解析                          │
│                                                         │
│ 2. Digest 变更处理流程                                   │
│    ├─ 拉取最新 latest 镜像                              │
│    ├─ 解析 paths.yaml 为 UpgradePath CR                │
│    ├─ 校验路径合法性 (环检测、版本格式)                  │
│    ├─ 更新内存图索引 (UpgradePathGraph)                 │
│    ├─ 同步至 Kubernetes CR (单 CR 更新)                 │
│    └─ 更新 lastKnownDigest 与 lastCheckedAt             │
│                                                         │
│ 3. 异常处理                                             │
│    ├─ 拉取失败 → 保留旧 digest，记录 Warning 事件        │
│    ├─ 解析失败 → 标记 CR Status.Phase=Invalid           │
│    └─ 连续 3 次失败 → 发送告警通知                       │
└─────────────────────────────────────────────────────────┘
```

**代码实现**：

```go
// pkg/upgrade/digest_monitor.go
type DigestMonitor struct {
    mu              sync.RWMutex
    ociRef          string
    lastKnownDigest  string
    lastCheckedAt    time.Time
    ociClient       *oci.Client
    checkInterval   time.Duration
    stopCh          chan struct{}
    onDigestChange  func(newDigest string, paths []UpgradePathEdge) error
}

func (m *DigestMonitor) Start(ctx context.Context) error {
    // 首次检查
    if err := m.checkDigest(ctx); err != nil {
        return err
    }
    
    ticker := time.NewTicker(m.checkInterval)
    go func() {
        for {
            select {
            case <-ticker.C:
                _ = m.checkDigest(ctx)
            case <-m.stopCh:
                ticker.Stop()
                return
            }
        }
    }()
    return nil
}

func (m *DigestMonitor) checkDigest(ctx context.Context) error {
    currentDigest, err := m.ociClient.GetDigest(m.ociRef)
    if err != nil {
        return fmt.Errorf("failed to get digest: %w", err)
    }
    
    m.mu.RLock()
    if currentDigest == m.lastKnownDigest {
        m.mu.RUnlock()
        return nil // digest 未变更
    }
    m.mu.RUnlock()
    
    // Digest 变更，拉取最新镜像
    img, err := m.ociClient.Pull(m.ociRef)
    if err != nil {
        return err
    }
    
    layer, err := img.GetLayerByPath("paths.yaml")
    if err != nil {
        return err
    }
    
    var up cvoapi.UpgradePath
    if err := yaml.Unmarshal(layer.Content, &up); err != nil {
        return err
    }
    
    // 解析路径边
    edges := make([]UpgradePathEdge, len(up.Spec.Paths))
    for i, p := range up.Spec.Paths {
        edges[i] = UpgradePathEdge{From: p.From, To: p.To, Blocked: p.Blocked}
    }
    
    // 触发回调更新图
    if m.onDigestChange != nil {
        if err := m.onDigestChange(currentDigest, edges); err != nil {
            return err
        }
    }
    
    m.mu.Lock()
    m.lastKnownDigest = currentDigest
    m.lastCheckedAt = time.Now()
    m.mu.Unlock()
    
    return nil
}
```

#### 4.4.4 UpgradePath 数据结构设计

**单 CR 聚合设计**：所有升级路径定义在单个 `UpgradePath` CR 中，通过 `spec.paths` 数组存储多条路径边。

**CRD 定义**：

```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: UpgradePath
metadata:
  name: openfuyao-upgrade-paths
spec:
  ociRef: "registry/openfuyao-upgradepath:latest"
  paths:
    - from: "v2.4.0"
      to: "v2.5.0"
      blocked: false
      deprecated: false
    - from: "v2.5.0"
      to: "v2.6.0"
      blocked: false
      deprecated: false
      preCheck:
        - name: "etcd-backup"
          required: true
      postCheck:
        - name: "cluster-health"
          required: true
    - from: "v2.4.0"
      to: "v2.6.0"
      blocked: true
      notes: "Direct upgrade blocked, please upgrade via v2.5.0"
status:
  phase: Active
  lastDigest: "sha256:abc123..."
  lastCheckedAt: "2026-05-09T10:00:00Z"
  pathCount: 3
```

**Go 结构体定义**：

```go
type UpgradePathSpec struct {
    OCIRef string              `json:"ociRef"`
    Paths  []UpgradePathEdge   `json:"paths"`
}

// UpgradePathEdge 表示图中的一条边（单条升级路径）
type UpgradePathEdge struct {
    From        string        `json:"from"`
    To          string        `json:"to"`
    Blocked     bool          `json:"blocked,omitempty"`
    Deprecated  bool          `json:"deprecated,omitempty"`
    PreCheck    []CheckStep   `json:"preCheck,omitempty"`
    PostCheck   []CheckStep   `json:"postCheck,omitempty"`
    Notes       string        `json:"notes,omitempty"`
}

type CheckStep struct {
    Name     string `json:"name"`
    Required bool   `json:"required,omitempty"`
}

type UpgradePathStatus struct {
    Phase         UpgradePathPhase `json:"phase"`
    LastDigest    string           `json:"lastDigest,omitempty"`
    LastCheckedAt *metav1.Time     `json:"lastCheckedAt,omitempty"`
    PathCount     int              `json:"pathCount,omitempty"`
    Conditions    []metav1.Condition `json:"conditions,omitempty"`
}

type UpgradePathPhase string

const (
    PhaseActive     UpgradePathPhase = "Active"
    PhaseBlocked    UpgradePathPhase = "Blocked"
    PhaseInvalid    UpgradePathPhase = "Invalid"
)
```

**UpgradePathEdge 设计说明**：

- 每条 `UpgradePathEdge` 代表升级图中的一条有向边
- `From` 和 `To` 定义边的起点和终点（版本节点）
- `Blocked` 标记该边是否被拦截
- `Deprecated` 标记该路径是否已废弃（仍可用但不推荐）
- `PreCheck/PostCheck` 定义升级前后的检查步骤

### 4.5 升级路径与兼容性算法设计

**组件扁平化与兼容性检查算法**：

1. **收集与扁平化**：遍历 `ReleaseImage` 中所有组件，若为 `composite` 类型，递归展开 `subComponents` 至叶子组件列表 `FlatList`。
2. **构建约束图**：为 `FlatList` 中每个组件解析 `compatibility.constraints`，构建有向约束图 `G=(V,E)`。
3. **SAT 求解具体过程**：
   - **变量转换**：将每个组件版本转换为语义化版本约束集合。例如 `etcd: ">=3.5.10"` 转换为区间 `[3.5.10, ∞)`。
   - **区间求交**：对同一组件的多个约束进行区间交集运算。若交集为空，则直接判定冲突。
   - **依赖图遍历**：按拓扑顺序遍历组件，将已解析的版本代入后续组件的约束中。若出现 `A requires B>=x` 但 `B` 已锁定为 `<x` 的版本，则触发回溯或报错。
   - **最终赋值**：若所有约束均满足且无环，返回 `Valid`；否则返回冲突组件对与具体规则。
4. **预检拦截**：在 `ReleaseImageReconciler` 中执行静态校验；在 `BKEClusterReconciler` DAG 执行前执行运行时校验。

**兼容性算法流程图**：

```txt
Start
  │
  ▼
┌─────────────────────────────┐
│ 1. 扁平化 ReleaseImage      │
│    展开所有 composite       │
│    生成 FlatList            │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ 2. 提取 Compatibility       │
│    构建约束矩阵              │
│    G = (V, E_constraints)   │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ 3. SAT 求解引擎             │
│    ├─ 语义化版本区间求交    │
│    ├─ 约束冲突检测          │
│    └─ 依赖环检测            │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ 4. 结果判定                  │
│    ├─ 无冲突 → Valid        │
│    └─ 有冲突 → Invalid      │
│       返回冲突路径与规则     │
└─────────────────────────────┘
```

#### 4.5.1 SAT/CSP 兼容性求解设计

**选型**：采用 `github.com/Masterminds/semver/v3` 进行语义化版本解析与约束匹配，结合轻量级 **CSP（约束满足问题）回溯算法**。K8s 生态中版本依赖本质是 CSP 而非布尔 SAT，回溯求解更贴合实际。

**规则语法**：
支持语义化版本约束：`>=`, `<=`, `>`, `<`, `=`, `!=`, 区间 `>=1.0.0 <2.0.0`。

**使用方法**：

1. **变量定义**：每个组件为一个变量，定义域为其可用版本列表。
2. **约束转换**：将 `rule: ">=3.5.10"` 转换为 `semver.Constraints` 对象。
3. **拓扑排序**：按 `dependencies` 构建有向无环图，确定变量赋值顺序。
4. **约束传播与回溯**：
   - 按拓扑序依次为组件分配版本。
   - 每次分配后，检查该组件的 `constraints` 是否与已分配的其他组件版本冲突。
   - 若冲突，回溯至上一个组件尝试其他版本；若所有版本均冲突，返回 `Invalid` 并输出冲突路径。

**代码实现**：

```go
func CheckCompatibility(components []ComponentRef, manifestStore *ManifestStore) error {
    // 1. 解析所有组件的 constraints
    constraints := make(map[string][]*semver.Constraints)
    for _, comp := range components {
        cv := manifestStore.Get(comp.Name, comp.Version)
        for _, c := range cv.Spec.Compatibility.Constraints {
            constraint, err := semver.NewConstraint(c.Rule)
            if err != nil { return err }
            constraints[c.Component] = append(constraints[c.Component], constraint)
        }
    }
    // 2. 校验实际版本是否满足约束
    for compName, consList := range constraints {
        actualVer := getComponentVersion(compName) // 从集群状态获取
        ver, err := semver.NewVersion(actualVer)
        if err != nil { return err }
        for _, c := range consList {
            if !c.Check(ver) {
                return fmt.Errorf("component %s version %s violates constraint %s", compName, actualVer, c.Original())
            }
        }
    }
    return nil
}
```

#### 4.5.2 manifestStore 实现

**设计思路**：`ManifestStore` 作为版本清单的统一访问层，承担以下职责

1. **抽象存储后端**：屏蔽本地文件系统与 OCI 远程仓库的差异，提供统一的组件清单获取接口
2. **多级缓存策略**：采用 `sync.Map` 实现内存缓存，避免重复解析 YAML 和拉取 OCI 镜像
3. **降级机制**：优先从本地 `/etc/bke/manifests` 加载，失败后降级至 OCI 拉取，支持离线环境运行
4. **线程安全**：所有缓存操作使用原子操作，支持高并发 Reconcile 循环访问
5. **多文件支持**：组件目录下包含 `component.yaml`（元数据）与多个资源清单文件（rbac.yaml、configmap.yaml、deployment.yaml 等）
6. **模板渲染**：所有 YAML 文件支持 Go template 语法，加载时统一渲染

**组件目录结构**：

```txt
bke-manifests/
└── kubernetes/
    └── v1.29.0/
        ├── component.yaml          # 组件元数据（版本、依赖、策略）
        ├── 01-crd.yaml             # CRD 定义（数字前缀控制顺序）
        ├── 02-rbac.yaml            # RBAC 资源（ServiceAccount/Role/ClusterRoleBinding）
        ├── 03-configmap.yaml       # ConfigMap 配置
        ├── 04-secret.yaml          # Secret 资源（可选）
        ├── 05-deployment.yaml      # Deployment/StatefulSet/DaemonSet
        └── 06-service.yaml         # Service 资源
```

**文件命名与排序规则**：

清单文件按**文件名称自然序（字母顺序）**排序后依次应用。推荐使用**数字前缀**明确控制应用顺序，确保依赖关系正确：

| 前缀 | 文件示例 | 说明 |
| ---- | -------- | ---- |
| `01-` | `01-crd.yaml` | CRD 优先创建，后续资源才能引用自定义资源类型 |
| `02-` | `02-rbac.yaml` | RBAC 权限配置，为后续工作负载提供访问控制 |
| `03-` | `03-configmap.yaml` | ConfigMap 配置，工作负载启动前需就绪 |
| `04-` | `04-secret.yaml` | Secret 敏感数据，如证书、密钥 |
| `05-` | `05-deployment.yaml` | Deployment/StatefulSet/DaemonSet 工作负载 |
| `06-` | `06-service.yaml` | Service/Ingress 网络暴露 |

**设计优势**：
- **灵活性**：清单维护者可通过调整文件名前缀自由控制应用顺序，无需修改代码
- **可读性**：文件名即文档，目录结构清晰展示资源创建顺序
- **可扩展**：新增资源类型只需添加文件并分配合适前缀，无需更新硬编码优先级表

**架构设计**：

```txt
┌─────────────────────────────────────────────────────────┐
│                    Caller (Reconciler)                  │
└────────────────────────┬────────────────────────────────┘
                         │ GetComponentManifests(name, version, ctx)
                         ▼
┌─────────────────────────────────────────────────────────┐
│                   ManifestStore                         │
│  ┌───────────────────────────────────────────────────┐  │
│  │ 1. 检查内存缓存 (sync.Map)                         │  │
│  │    ├─ Hit → 克隆副本                              │  │
│  │    └─ Miss → 继续                                 │  │
│  └───────────────────────┬───────────────────────────┘  │
│                          │                               │
│  ┌───────────────────────▼───────────────────────────┐  │
│  │ 2. 尝试本地文件系统                                │  │
│  │    /etc/bke/manifests/{name}/{version}/           │  │
│  │    ├─ 扫描目录下所有 *.yaml 文件                   │  │
│  │    ├─ 解析 component.yaml 为元数据                 │  │
│  │    ├─ 解析其余 YAML 为资源清单列表                 │  │
│  │    └─ 写入缓存 → 继续                             │  │
│  └───────────────────────┬───────────────────────────┘  │
│                          │                               │
│  ┌───────────────────────▼───────────────────────────┐  │
│  │ 3. OCI 远程拉取                                   │  │
│  │    registry/openfuyao-release:{version}           │  │
│  │    ├─ 拉取镜像 → 提取组件目录 layer                │  │
│  │    ├─ 遍历目录下所有 *.yaml 文件                   │  │
│  │    ├─ 写入缓存 → 继续                             │  │
│  │    └─ 失败 → 返回错误                             │  │
│  └───────────────────────┬───────────────────────────┘  │
│                          │                               │
│  ┌───────────────────────▼───────────────────────────┐  │
│  │ 4. 模板渲染 (若传入 TemplateContext)               │  │
│  │    ├─ 渲染 component.yaml 元数据                   │  │
│  │    ├─ 按依赖顺序排序资源清单 (CRD→RBAC→CM→Deploy) │  │
│  │    ├─ 逐个渲染 {{.xxx}} 占位符                     │  │
│  │    └─ 返回渲染后的清单列表                         │  │
│  └───────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

**代码实现**：

```go
// pkg/manifest/store.go

// ResourceManifest 表示单个渲染后的资源清单
type ResourceManifest struct {
    FileName string // 原始文件名 (rbac.yaml, deployment.yaml 等)
    Content  []byte // 渲染后的 YAML 内容
    Objects  []unstructured.Unstructured // 解析后的 K8s 对象列表
}

// ComponentPackage 组件完整包（元数据 + 资源清单列表）
type ComponentPackage struct {
    Metadata  *ComponentVersion   // component.yaml 元数据
    Manifests []ResourceManifest  // 按依赖顺序排序的资源清单
}

// ManifestStore 清单存储
type ManifestStore struct {
    cache     *sync.Map // key: "{name}@{version}" -> *ComponentPackage
    ociClient *oci.Client
    localPath string    // 本地 manifest 根目录，默认 /etc/bke/manifests
}

// GetComponentManifests 获取组件完整包（含元数据与所有资源清单）
func (s *ManifestStore) GetComponentManifests(name, version string, ctx *TemplateContext) (*ComponentPackage, error) {
    key := fmt.Sprintf("%s@%s", name, version)
    
    // 1. 尝试从缓存加载
    if val, ok := s.cache.Load(key); ok {
        pkg := val.(*ComponentPackage)
        if ctx != nil {
            return s.renderPackage(pkg, ctx)
        }
        return pkg, nil
    }
    
    // 2. 尝试从本地 bke-manifests 目录加载
    compDir := fmt.Sprintf("%s/%s/%s", s.localPath, name, version)
    if _, err := os.Stat(compDir); err == nil {
        pkg, err := s.loadFromLocal(compDir)
        if err == nil {
            s.cache.Store(key, pkg)
            if ctx != nil {
                return s.renderPackage(pkg, ctx)
            }
            return pkg, nil
        }
    }
    
    // 3. 降级：从 OCI 拉取组件目录
    img, err := s.ociClient.Pull(fmt.Sprintf("registry/openfuyao-release:%s", version))
    if err != nil { return nil, err }
    
    pkg, err := s.loadFromOCI(img, name, version)
    if err != nil { return nil, err }
    
    s.cache.Store(key, pkg)
    if ctx != nil {
        return s.renderPackage(pkg, ctx)
    }
    return pkg, nil
}

// loadFromLocal 从本地目录加载组件包
func (s *ManifestStore) loadFromLocal(compDir string) (*ComponentPackage, error) {
    entries, err := os.ReadDir(compDir)
    if err != nil { return nil, err }
    
    pkg := &ComponentPackage{}
    var manifestFiles []string
    var chartFiles map[string][]byte
    var binaryFiles map[string][]byte
    
    // 第一遍扫描：加载 component.yaml 确定组件类型
    for _, entry := range entries {
        if entry.Name() == "component.yaml" {
            filePath := filepath.Join(compDir, entry.Name())
            data, err := os.ReadFile(filePath)
            if err != nil { return nil, err }
            
            cv := &ComponentVersion{}
            if err := yaml.Unmarshal(data, cv); err != nil {
                return nil, fmt.Errorf("failed to parse component.yaml: %w", err)
            }
            pkg.Metadata = cv
            pkg.Type = cv.Spec.Type // 从元数据中获取组件类型
            break
        }
    }
    
    if pkg.Metadata == nil {
        return nil, fmt.Errorf("component.yaml not found in %s", compDir)
    }
    
    // 第二遍扫描：根据类型加载对应资源
    for _, entry := range entries {
        if entry.Name() == "component.yaml" {
            continue
        }
        
        filePath := filepath.Join(compDir, entry.Name())
        data, err := os.ReadFile(filePath)
        if err != nil { continue }
        
        switch pkg.Type {
        case ComponentTypeYAML:
            if strings.HasSuffix(entry.Name(), ".yaml") {
                manifestFiles = append(manifestFiles, entry.Name())
            }
        case ComponentTypeHelm:
            if chartFiles == nil {
                chartFiles = make(map[string][]byte)
            }
            chartFiles[entry.Name()] = data
        case ComponentTypeBinary:
            if binaryFiles == nil {
                binaryFiles = make(map[string][]byte)
            }
            binaryFiles[entry.Name()] = data
        case ComponentTypeInline:
            // Inline 类型不需要额外文件，Phase 代码已注册
        }
    }
    
    // 根据类型构建 ComponentPackage
    switch pkg.Type {
    case ComponentTypeYAML:
        pkg.Manifests = s.sortManifests(manifestFiles, compDir)
    case ComponentTypeHelm:
        pkg.Chart = s.parseHelmChart(chartFiles, pkg.Metadata)
    case ComponentTypeBinary:
        pkg.Binary = s.parseBinaryArtifact(binaryFiles, pkg.Metadata)
    case ComponentTypeInline:
        pkg.Inline = pkg.Metadata.Spec.Inline
    }
    
    return pkg, nil
}

// loadFromOCI 从 OCI 镜像加载组件包
func (s *ManifestStore) loadFromOCI(img *oci.Image, name, version string) (*ComponentPackage, error) {
    prefix := fmt.Sprintf("%s/%s/", name, version)
    layers, err := img.GetLayersByPrefix(prefix)
    if err != nil { return nil, err }
    
    pkg := &ComponentPackage{}
    var manifestFiles []string
    var chartFiles map[string][]byte
    var binaryFiles map[string][]byte
    
    // 第一遍扫描：加载 component.yaml 确定组件类型
    for _, layer := range layers {
        fileName := strings.TrimPrefix(layer.Path, prefix)
        if fileName == "component.yaml" {
            cv := &ComponentVersion{}
            if err := yaml.Unmarshal(layer.Content, cv); err != nil {
                return nil, fmt.Errorf("failed to parse component.yaml: %w", err)
            }
            pkg.Metadata = cv
            pkg.Type = cv.Spec.Type // 从元数据中获取组件类型
            break
        }
    }
    
    if pkg.Metadata == nil {
        return nil, fmt.Errorf("component.yaml not found in OCI image")
    }
    
    // 第二遍扫描：根据类型加载对应资源
    for _, layer := range layers {
        fileName := strings.TrimPrefix(layer.Path, prefix)
        if fileName == "component.yaml" {
            continue
        }
        
        switch pkg.Type {
        case ComponentTypeYAML:
            if strings.HasSuffix(fileName, ".yaml") {
                manifestFiles = append(manifestFiles, fileName)
                s.cacheManifest(fmt.Sprintf("%s@%s:%s", name, version, fileName), layer.Content)
            }
        case ComponentTypeHelm:
            if chartFiles == nil {
                chartFiles = make(map[string][]byte)
            }
            chartFiles[fileName] = layer.Content
        case ComponentTypeBinary:
            if binaryFiles == nil {
                binaryFiles = make(map[string][]byte)
            }
            binaryFiles[fileName] = layer.Content
        case ComponentTypeInline:
            // Inline 类型不需要额外文件
        }
    }
    
    // 根据类型构建 ComponentPackage
    switch pkg.Type {
    case ComponentTypeYAML:
        pkg.Manifests = s.sortManifests(manifestFiles, "")
    case ComponentTypeHelm:
        pkg.Chart = s.parseHelmChart(chartFiles, pkg.Metadata)
    case ComponentTypeBinary:
        pkg.Binary = s.parseBinaryArtifact(binaryFiles, pkg.Metadata)
    case ComponentTypeInline:
        pkg.Inline = pkg.Metadata.Spec.Inline
    }
    
    return pkg, nil
}

// parseHelmChart 解析 Helm Chart 文件
func (s *ManifestStore) parseHelmChart(files map[string][]byte, metadata *ComponentVersion) *HelmChart {
    chart := &HelmChart{
        Name:    metadata.Spec.Name,
        Version: metadata.Spec.Version,
        Files:   files,
    }
    
    // 解析 values.yaml
    if valuesData, ok := files["values.yaml"]; ok {
        values := make(map[string]interface{})
        if err := yaml.Unmarshal(valuesData, &values); err == nil {
            chart.Values = values
        }
    }
    
    // 解析 Chart.yaml 获取仓库 URL
    if chartData, ok := files["Chart.yaml"]; ok {
        chartMeta := make(map[string]interface{})
        if err := yaml.Unmarshal(chartData, &chartMeta); err == nil {
            if repoURL, ok := chartMeta["repository"].(string); ok {
                chart.RepoURL = repoURL
            }
        }
    }
    
    return chart
}

// parseBinaryArtifact 解析二进制安装包
func (s *ManifestStore) parseBinaryArtifact(files map[string][]byte, metadata *ComponentVersion) *BinaryArtifact {
    binary := &BinaryArtifact{
        Name:    metadata.Spec.Name,
        Version: metadata.Spec.Version,
    }
    
    // 解析 binary.yaml 元数据
    if binaryData, ok := files["binary.yaml"]; ok {
        if err := yaml.Unmarshal(binaryData, binary); err != nil {
            // 使用默认值
        }
    }
    
    // 查找安装脚本
    for name, content := range files {
        if name == "install.sh" || name == "install" {
            binary.InstallScript = string(content)
        }
        if strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".zip") {
            // 二进制压缩包，保存到临时文件
            tmpFile, _ := os.CreateTemp("", "binary-*")
            tmpFile.Write(content)
            binary.LocalPath = tmpFile.Name()
        }
    }
    
    return binary
}

// sortManifests 按文件名称自然序排序清单文件
func (s *ManifestStore) sortManifests(files []string, baseDir string) []ResourceManifest {
    // 读取文件内容并解析
    var manifests []ResourceManifest
    for _, file := range files {
        var data []byte
        if baseDir != "" {
            data, _ = os.ReadFile(filepath.Join(baseDir, file))
        }
        
        // 解析 YAML 为 unstructured 对象
        objects, _ := parseYAMLObjects(data)
        
        manifests = append(manifests, ResourceManifest{
            FileName: file,
            Content:  data,
            Objects:  objects,
        })
    }
    
    // 按文件名称自然序排序（字母顺序）
    // 文件名建议使用前缀数字控制顺序，如 01-crd.yaml, 02-rbac.yaml
    sort.Slice(manifests, func(i, j int) bool {
        return manifests[i].FileName < manifests[j].FileName
    })
    
    return manifests
}

// renderPackage 渲染整个组件包
func (s *ManifestStore) renderPackage(pkg *ComponentPackage, ctx *TemplateContext) (*ComponentPackage, error) {
    rendered := &ComponentPackage{
        Metadata:  pkg.Metadata,
        Manifests: make([]ResourceManifest, len(pkg.Manifests)),
    }
    
    // 渲染元数据
    if pkg.Metadata != nil {
        rendered.Metadata, _ = s.renderTemplate(pkg.Metadata, ctx)
    }
    
    // 渲染每个清单文件
    for i, manifest := range pkg.Manifests {
        renderedContent, err := s.renderYAMLContent(manifest.Content, ctx)
        if err != nil {
            return nil, fmt.Errorf("failed to render %s: %w", manifest.FileName, err)
        }
        
        objects, _ := parseYAMLObjects(renderedContent)
        rendered.Manifests[i] = ResourceManifest{
            FileName: manifest.FileName,
            Content:  renderedContent,
            Objects:  objects,
        }
    }
    
    return rendered, nil
}

// parseYAMLObjects 解析 YAML 内容为 K8s 对象列表（支持多文档）
func parseYAMLObjects(data []byte) ([]unstructured.Unstructured, error) {
    var objects []unstructured.Unstructured
    docs := bytes.Split(data, []byte("\n---\n"))
    
    for _, doc := range docs {
        if len(bytes.TrimSpace(doc)) == 0 {
            continue
        }
        
        var obj unstructured.Unstructured
        if err := yaml.Unmarshal(doc, &obj); err == nil && obj.GetKind() != "" {
            objects = append(objects, obj)
        }
    }
    
    return objects, nil
}

// renderTemplate 渲染 ComponentVersion 元数据模板
func (s *ManifestStore) renderTemplate(cv *ComponentVersion, ctx *TemplateContext) (*ComponentVersion, error) {
    data, err := yaml.Marshal(cv)
    if err != nil {
        return nil, err
    }
    
    renderedData, err := s.renderYAMLContent(data, ctx)
    if err != nil {
        return nil, err
    }
    
    rendered := &ComponentVersion{}
    if err := yaml.Unmarshal(renderedData, rendered); err != nil {
        return nil, fmt.Errorf("failed to unmarshal rendered template: %w", err)
    }
    
    return rendered, nil
}

// renderYAMLContent 渲染任意 YAML 内容模板（通用方法）
func (s *ManifestStore) renderYAMLContent(data []byte, ctx *TemplateContext) ([]byte, error) {
    if ctx == nil || len(data) == 0 {
        return data, nil
    }
    
    tmpl, err := template.New("manifest").Funcs(template.FuncMap{
        "default": func(val, def string) string {
            if val == "" { return def }
            return val
        },
        "lower": strings.ToLower,
        "upper": strings.ToUpper,
    }).Parse(string(data))
    if err != nil {
        return nil, fmt.Errorf("failed to parse template: %w", err)
    }
    
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, ctx); err != nil {
        return nil, fmt.Errorf("failed to render template: %w", err)
    }
    
    return buf.Bytes(), nil
}

func (s *ManifestStore) GetReleaseImage(version string) (*ReleaseImage, error) {
    key := fmt.Sprintf("release-image@%s", version)
    if val, ok := s.cache.Load(key); ok {
        return val.(*ReleaseImage), nil
    }
    
    // 1. 尝试从本地加载 release.yaml
    localPath := fmt.Sprintf("%s/release-%s.yaml", s.localPath, version)
    if data, err := os.ReadFile(localPath); err == nil {
        ri := &ReleaseImage{}
        if err := yaml.Unmarshal(data, ri); err == nil {
            s.cache.Store(key, ri)
            return ri, nil
        }
    }
    
    // 2. 降级：从 OCI 拉取
    img, err := s.ociClient.Pull(fmt.Sprintf("registry/openfuyao-release:%s", version))
    if err != nil { return nil, err }
    
    // 优先读取 release.yaml layer，否则解析 config
    layer, err := img.GetLayerByPath("release.yaml")
    if err != nil {
        // fallback: 解析 OCI config 为 ReleaseImage
        cfg, err := img.ConfigFile()
        if err != nil { return nil, err }
        ri := &ReleaseImage{}
        if err := json.Unmarshal(cfg.Config.Labels["release-spec"], ri); err != nil {
            return nil, err
        }
        s.cache.Store(key, ri)
        return ri, nil
    }
    
    ri := &ReleaseImage{}
    if err := yaml.Unmarshal(layer.Content, ri); err != nil { return nil, err }
    
    s.cache.Store(key, ri)
    return ri, nil
}
```

**ApplyManifests 应用接口**：

```go
// pkg/manifest/applier.go

// ManifestApplier 清单应用器
type ManifestApplier struct {
    client client.Client
    logger logr.Logger
}

// ApplyManifests 按顺序应用组件清单（等价于 kubectl apply）
func (a *ManifestApplier) ApplyManifests(ctx context.Context, manifests []ResourceManifest) error {
    for _, manifest := range manifests {
        a.logger.Info("Applying manifest", "file", manifest.FileName)
        
        for _, obj := range manifest.Objects {
            if err := a.applyObject(ctx, &obj); err != nil {
                return fmt.Errorf("failed to apply %s/%s: %w", 
                    obj.GetKind(), obj.GetName(), err)
            }
        }
    }
    return nil
}

// applyObject 应用单个对象（幂等操作）
func (a *ManifestApplier) applyObject(ctx context.Context, obj *unstructured.Unstructured) error {
    // 设置注解标记用于追踪
    annotations := obj.GetAnnotations()
    if annotations == nil {
        annotations = make(map[string]string)
    }
    annotations["cvo.openfuyao.cn/managed-by"] = "manifest-store"
    obj.SetAnnotations(annotations)
    
    // 尝试更新，若不存在则创建
    existing := &unstructured.Unstructured{}
    existing.SetGroupVersionKind(obj.GroupVersionKind())
    err := a.client.Get(ctx, client.ObjectKey{
        Namespace: obj.GetNamespace(),
        Name:      obj.GetName(),
    }, existing)
    
    if err != nil && apierrors.IsNotFound(err) {
        return a.client.Create(ctx, obj)
    }
    
    // 保留不可变字段
    obj.SetResourceVersion(existing.GetResourceVersion())
    obj.SetUID(existing.GetUID())
    return a.client.Update(ctx, obj)
}

// DeleteManifests 删除组件清单（用于回滚或卸载）
func (a *ManifestApplier) DeleteManifests(ctx context.Context, manifests []ResourceManifest) error {
    // 反向删除（与创建顺序相反）
    for i := len(manifests) - 1; i >= 0; i-- {
        manifest := manifests[i]
        for _, obj := range manifest.Objects {
            if err := a.client.Delete(ctx, &obj); err != nil && !apierrors.IsNotFound(err) {
                return err
            }
        }
    }
    return nil
}
```

#### 4.5.2.1 多类型组件安装器架构

**设计思路**：bke-manifests 中的组件可能为四种类型，需要统一的安装器接口支持扩展：

| 类型 | 标识 | 说明 | 安装方式 |
| ---- | ---- | ---- | -------- |
| **YAML 清单** | `yaml` | 标准 Kubernetes 资源清单文件 | `kubectl apply` 等价操作 |
| **Helm Chart** | `helm` | Helm 格式的 Chart 包 | Helm SDK 安装/升级/回滚 |
| **Inline Phase** | `inline` | 内嵌 Go 代码 Phase 处理器 | ComponentFactory 注册调用 |
| **二进制安装包** | `binary` | 预编译二进制或脚本 | 下载、解压、执行安装脚本 |

**组件类型定义**：

```go
// pkg/manifest/types.go

// ComponentType 组件安装类型
type ComponentType string

const (
    ComponentTypeYAML    ComponentType = "yaml"    // k8s yaml清单
    ComponentTypeHelm    ComponentType = "helm"    // helm chart
    ComponentTypeInline  ComponentType = "inline"  // inline Phase
    ComponentTypeBinary  ComponentType = "binary"  // 二进制安装包
)

// ComponentPackage 组件完整包（支持多类型）
type ComponentPackage struct {
    Metadata  *ComponentVersion   // component.yaml 元数据
    Type      ComponentType       // 组件类型
    
    // YAML 类型字段
    Manifests []ResourceManifest  // 资源清单列表
    
    // Helm 类型字段
    Chart     *HelmChart          // Helm chart包
    
    // Binary 类型字段
    Binary    *BinaryArtifact     // 二进制制品
    
    // Inline 类型字段
    Inline    *InlineSpec         // Phase规范
}

// HelmChart Helm chart包
type HelmChart struct {
    Name       string                 // chart名称
    Version    string                 // chart版本
    Values     map[string]interface{} // 自定义values
    Files      map[string][]byte      // chart文件内容
    RepoURL    string                 // chart仓库URL（可选）
}

// BinaryArtifact 二进制安装包
type BinaryArtifact struct {
    Name          string            // 包名称
    Version       string            // 版本
    Checksum      string            // SHA256校验值
    DownloadURL   string            // 下载URL
    LocalPath     string            // 本地缓存路径
    InstallScript string            // 安装脚本内容
    Arch          string            // 目标架构 (amd64/arm64)
    OS            string            // 目标操作系统 (linux)
}
```

**统一安装器接口**：

```go
// pkg/manifest/installer.go

// ComponentInstaller 组件安装器接口
type ComponentInstaller interface {
    // Type 返回支持的组件类型
    Type() ComponentType
    
    // Install 安装组件
    Install(ctx context.Context, pkg *ComponentPackage, vCtx *VersionContext) error
    
    // Uninstall 卸载组件
    Uninstall(ctx context.Context, pkg *ComponentPackage) error
    
    // HealthCheck 检查组件健康状态
    HealthCheck(ctx context.Context, pkg *ComponentPackage) error
}

// InstallerRegistry 安装器注册表
type InstallerRegistry struct {
    mu         sync.RWMutex
    installers map[ComponentType]ComponentInstaller
}

func NewInstallerRegistry() *InstallerRegistry {
    return &InstallerRegistry{
        installers: make(map[ComponentType]ComponentInstaller),
    }
}

func (r *InstallerRegistry) Register(installer ComponentInstaller) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.installers[installer.Type()] = installer
}

func (r *InstallerRegistry) GetInstaller(t ComponentType) (ComponentInstaller, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    installer, ok := r.installers[t]
    if !ok {
        return nil, fmt.Errorf("installer not found for type: %s", t)
    }
    return installer, nil
}

// InstallComponent 统一安装入口
func (r *InstallerRegistry) InstallComponent(ctx context.Context, pkg *ComponentPackage, vCtx *VersionContext) error {
    installer, err := r.GetInstaller(pkg.Type)
    if err != nil {
        return err
    }
    return installer.Install(ctx, pkg, vCtx)
}
```

**四种安装器实现**：

**1. YAML 清单安装器**：

```go
// pkg/manifest/yaml_installer.go

type YamlInstaller struct {
    client  client.Client
    logger  logr.Logger
    applier *ManifestApplier
}

func (i *YamlInstaller) Type() ComponentType {
    return ComponentTypeYAML
}

func (i *YamlInstaller) Install(ctx context.Context, pkg *ComponentPackage, vCtx *VersionContext) error {
    if len(pkg.Manifests) == 0 {
        return fmt.Errorf("no manifests found in package %s", pkg.Metadata.Name)
    }
    
    i.logger.Info("Installing YAML manifests", "component", pkg.Metadata.Name)
    return i.applier.ApplyManifests(ctx, pkg.Manifests)
}

func (i *YamlInstaller) Uninstall(ctx context.Context, pkg *ComponentPackage) error {
    return i.applier.DeleteManifests(ctx, pkg.Manifests)
}

func (i *YamlInstaller) HealthCheck(ctx context.Context, pkg *ComponentPackage) error {
    // 检查所有资源是否处于Ready状态
    for _, manifest := range pkg.Manifests {
        for _, obj := range manifest.Objects {
            if err := i.checkObjectReady(ctx, &obj); err != nil {
                return err
            }
        }
    }
    return nil
}
```

**2. Helm Chart 安装器**：

```go
// pkg/manifest/helm_installer.go

type HelmInstaller struct {
    cfg    *action.Configuration
    logger logr.Logger
}

func (i *HelmInstaller) Type() ComponentType {
    return ComponentTypeHelm
}

func (i *HelmInstaller) Install(ctx context.Context, pkg *ComponentPackage, vCtx *VersionContext) error {
    if pkg.Chart == nil {
        return fmt.Errorf("no chart found in package %s", pkg.Metadata.Name)
    }
    
    i.logger.Info("Installing Helm chart", "chart", pkg.Chart.Name, "version", pkg.Chart.Version)
    
    // 构建 Helm values（合并 TemplateContext）
    values := pkg.Chart.Values
    if vCtx != nil {
        values = mergeValues(values, vCtx.ToHelmValues())
    }
    
    // 检查是否已安装，决定是 Install 还是 Upgrade
    histClient := action.NewHistory(i.cfg)
    _, err := histClient.Run(pkg.Chart.Name)
    
    var rel *release.Release
    if err == driver.ErrReleaseNotFound {
        // 首次安装
        installClient := action.NewInstall(i.cfg)
        installClient.ReleaseName = pkg.Chart.Name
        installClient.Namespace = vCtx.Namespace
        installClient.CreateNamespace = true
        installClient.Wait = true
        installClient.Timeout = pkg.Metadata.Spec.UpgradeStrategy.Timeout
        
        rel, err = installClient.Run(pkg.Chart, values)
    } else {
        // 升级
        upgradeClient := action.NewUpgrade(i.cfg)
        upgradeClient.Namespace = vCtx.Namespace
        upgradeClient.Wait = true
        upgradeClient.Timeout = pkg.Metadata.Spec.UpgradeStrategy.Timeout
        
        rel, err = upgradeClient.Run(pkg.Chart.Name, pkg.Chart, values)
    }
    
    if err != nil {
        return fmt.Errorf("helm install/upgrade failed: %w", err)
    }
    
    i.logger.Info("Helm chart installed", "release", rel.Name, "version", rel.Version)
    return nil
}

func (i *HelmInstaller) Uninstall(ctx context.Context, pkg *ComponentPackage) error {
    uninstallClient := action.NewUninstall(i.cfg)
    _, err := uninstallClient.Run(pkg.Chart.Name)
    return err
}

func (i *HelmInstaller) HealthCheck(ctx context.Context, pkg *ComponentPackage) error {
    statusClient := action.NewStatus(i.cfg)
    rel, err := statusClient.Run(pkg.Chart.Name)
    if err != nil {
        return err
    }
    
    if rel.Info.Status != release.StatusDeployed {
        return fmt.Errorf("helm release status: %s", rel.Info.Status)
    }
    return nil
}
```

**3. Inline Phase 安装器**：

```go
// pkg/manifest/inline_installer.go

type InlineInstaller struct {
    factory   *ComponentFactory
    logger    logr.Logger
}

func (i *InlineInstaller) Type() ComponentType {
    return ComponentTypeInline
}

func (i *InlineInstaller) Install(ctx context.Context, pkg *ComponentPackage, vCtx *VersionContext) error {
    if pkg.Inline == nil {
        return fmt.Errorf("no inline spec found in package %s", pkg.Metadata.Name)
    }
    
    // 从 ComponentFactory 获取 Phase 实例
    inst, err := i.factory.Resolve(
        pkg.Metadata.Spec.Name,
        pkg.Metadata.Spec.Version,
        pkg.Inline,
    )
    if err != nil {
        return fmt.Errorf("failed to resolve inline handler: %w", err)
    }
    
    // 注入上下文
    inst.Handler.SetVersionContext(vCtx)
    inst.Handler.SetComponentPackage(pkg)
    
    // 执行 Phase
    if !inst.Handler.NeedExecute(nil, nil) {
        i.logger.Info("Inline phase skipped", "component", pkg.Metadata.Name)
        return nil
    }
    
    i.logger.Info("Executing inline phase", "component", pkg.Metadata.Name, "handler", pkg.Inline.Handler)
    return inst.Handler.Execute(ctx)
}

func (i *InlineInstaller) Uninstall(ctx context.Context, pkg *ComponentPackage) error {
    // Inline 组件通常不需要卸载，或调用自定义卸载逻辑
    return nil
}

func (i *InlineInstaller) HealthCheck(ctx context.Context, pkg *ComponentPackage) error {
    // 调用自定义健康检查逻辑
    return nil
}
```

**4. 二进制安装包安装器**：

```go
// pkg/manifest/binary_installer.go

type BinaryInstaller struct {
    client    client.Client
    logger    logr.Logger
    cacheDir  string // 本地缓存目录
}

func (i *BinaryInstaller) Type() ComponentType {
    return ComponentTypeBinary
}

func (i *BinaryInstaller) Install(ctx context.Context, pkg *ComponentPackage, vCtx *VersionContext) error {
    if pkg.Binary == nil {
        return fmt.Errorf("no binary artifact found in package %s", pkg.Metadata.Name)
    }
    
    i.logger.Info("Installing binary package", "component", pkg.Metadata.Name)
    
    // 1. 下载或从缓存获取
    localPath, err := i.ensureBinaryCached(ctx, pkg.Binary)
    if err != nil {
        return fmt.Errorf("failed to cache binary: %w", err)
    }
    
    // 2. 执行安装脚本
    script := pkg.Binary.InstallScript
    if script == "" {
        script = defaultInstallScript
    }
    
    // 渲染脚本模板（注入变量）
    renderedScript, err := renderScriptTemplate(script, vCtx, localPath)
    if err != nil {
        return err
    }
    
    // 3. 在目标节点执行安装
    return i.executeInstallScript(ctx, renderedScript, vCtx)
}

func (i *BinaryInstaller) ensureBinaryCached(ctx context.Context, binary *BinaryArtifact) (string, error) {
    targetPath := filepath.Join(i.cacheDir, binary.Name, binary.Version, binary.Arch)
    
    // 检查缓存
    if _, err := os.Stat(targetPath); err == nil {
        return targetPath, nil
    }
    
    // 下载
    i.logger.Info("Downloading binary", "url", binary.DownloadURL)
    resp, err := http.Get(binary.DownloadURL)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    
    // 保存到临时文件
    tmpFile, err := os.CreateTemp("", "binary-*")
    if err != nil {
        return "", err
    }
    defer os.Remove(tmpFile.Name())
    
    if _, err := io.Copy(tmpFile, resp.Body); err != nil {
        return "", err
    }
    
    // 校验 checksum
    if err := verifyChecksum(tmpFile.Name(), binary.Checksum); err != nil {
        return "", err
    }
    
    // 解压（如果是压缩包）
    if err := extractArchive(tmpFile.Name(), targetPath); err != nil {
        return "", err
    }
    
    return targetPath, nil
}

func (i *BinaryInstaller) executeInstallScript(ctx context.Context, script string, vCtx *VersionContext) error {
    // 通过 SSH 或 Agent 在目标节点执行脚本
    // 这里简化为本地执行，实际应通过节点代理执行
    cmd := exec.CommandContext(ctx, "bash", "-c", script)
    cmd.Env = append(os.Environ(), i.buildInstallEnv(vCtx)...)
    
    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("install script failed: %w, output: %s", err, string(output))
    }
    
    i.logger.Info("Binary installation completed", "output", string(output))
    return nil
}

func (i *BinaryInstaller) Uninstall(ctx context.Context, pkg *ComponentPackage) error {
    // 执行卸载脚本
    return nil
}

func (i *BinaryInstaller) HealthCheck(ctx context.Context, pkg *ComponentPackage) error {
    // 检查二进制服务进程状态
    return nil
}
```

**安装器注册与初始化**：

```go
// cmd/manager/main.go

func setupInstallers(mgr ctrl.Manager) *manifest.InstallerRegistry {
    registry := manifest.NewInstallerRegistry()
    
    // 注册 YAML 安装器
    registry.Register(&manifest.YamlInstaller{
        Client:  mgr.GetClient(),
        Logger:  ctrl.Log.WithName("yaml-installer"),
        Applier: manifest.NewManifestApplier(mgr.GetClient()),
    })
    
    // 注册 Helm 安装器
    helmCfg := new(action.Configuration)
    // 初始化 Helm configuration...
    registry.Register(&manifest.HelmInstaller{
        Cfg:    helmCfg,
        Logger: ctrl.Log.WithName("helm-installer"),
    })
    
    // 注册 Inline 安装器
    registry.Register(&manifest.InlineInstaller{
        Factory: componentFactory,
        Logger:  ctrl.Log.WithName("inline-installer"),
    })
    
    // 注册二进制安装器
    registry.Register(&manifest.BinaryInstaller{
        Client:   mgr.GetClient(),
        Logger:   ctrl.Log.WithName("binary-installer"),
        CacheDir: "/var/cache/bke/binaries",
    })
    
    return registry
}
```

**架构设计图**：

```txt
┌─────────────────────────────────────────────────────────────────┐
│                    BKEClusterReconciler                         │
│                                                                 │
│  executeDAG()                                                   │
│    │                                                            │
│    ├─ GetComponentManifests(name, version, tmplCtx)             │
│    │   └─ 返回 ComponentPackage (含 Type 字段)                  │
│    │                                                            │
│    └─ installerRegistry.InstallComponent(ctx, pkg, vCtx) ◀──────┤
│        │                                                        │
│        └─ 根据 pkg.Type 路由到对应安装器                        │
└────────┬────────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────────┐
│                   InstallerRegistry                             │
│                                                                 │
│  installers: map[ComponentType]ComponentInstaller               │
│    │                                                            │
│    ├─ yaml    → YamlInstaller                                   │
│    ├─ helm    → HelmInstaller                                   │
│    ├─ inline  → InlineInstaller                                 │
│    └─ binary  → BinaryInstaller                                 │
└────────┬────────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────────┐
│                   ComponentInstaller 接口                       │
│                                                                 │
│  + Type() ComponentType                                         │
│  + Install(ctx, pkg, vCtx) error                                │
│  + Uninstall(ctx, pkg) error                                    │
│  + HealthCheck(ctx, pkg) error                                  │
└─────────────────────────────────────────────────────────────────┘
```

**扩展性设计**：

1. **插件化注册**：第三方组件类型可通过 `InstallerRegistry.Register()` 注册自定义安装器
2. **类型发现**：`ComponentVersion.Spec.Type` 字段声明组件类型，ManifestStore 自动识别
3. **降级策略**：若未找到对应安装器，返回明确错误并标记组件状态为 `InstallerNotFound`
4. **统一生命周期**：所有安装器实现 `Install/Uninstall/HealthCheck` 三阶段接口

#### 4.5.2.1.1 各类型组件样例

**类型 1：Inline Phase 组件**

```txt
bke-manifests/
└── pre-upgrade-resources/
    └── v1.0.0/
        └── component.yaml          # 仅包含元数据，Phase 代码已注册
```

```yaml
# component.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: pre-upgrade-resources-v1.0.0
spec:
  name: pre-upgrade-resources
  type: inline                      # 声明为 inline 类型
  version: v1.0.0
  inline:
    handler: EnsurePreUpgradeResources  # 已注册的 Phase 处理器名称
    version: v1.0
  dependencies: []                  # 无依赖，确保最先执行
  upgradeStrategy:
    mode: Batch
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

**类型 2：K8s YAML 清单组件**

```txt
bke-manifests/
└── etcd/
    └── v3.5.12/
        ├── component.yaml          # 元数据
        ├── 01-crd.yaml             # CRD 定义
        ├── 02-rbac.yaml            # RBAC 资源
        ├── 03-configmap.yaml       # ConfigMap 配置
        └── 04-statefulset.yaml     # StatefulSet 工作负载
```

```yaml
# component.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: etcd-v3.5.12
spec:
  name: etcd
  type: yaml                        # 声明为 yaml 类型
  version: v3.5.12
  dependencies: []
  upgradeStrategy:
    mode: Batch
    batchSize: 1
    timeout: "30m"
    failurePolicy: Rollback
```

```yaml
# 01-crd.yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: etcdbackups.etcd.openfuyao.cn
spec:
  group: etcd.openfuyao.cn
  versions:
    - name: v1
      served: true
      storage: true
  scope: Namespaced
  names:
    plural: etcdbackups
    singular: etcdbackup
    kind: EtcdBackup
---
# 02-rbac.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: etcd-manager
  namespace: {{.Namespace}}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: etcd-manager-role
rules:
  - apiGroups: [""]
    resources: ["pods", "services"]
    verbs: ["get", "list", "watch"]
---
# 03-configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: etcd-config
  namespace: {{.Namespace}}
data:
  ETCD_CLUSTER_SIZE: "{{.ControlPlaneReplicas}}"
  ETCD_REGION: "{{.Region}}"
---
# 04-statefulset.yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: etcd
  namespace: {{.Namespace}}
spec:
  replicas: {{.ControlPlaneReplicas}}
  selector:
    matchLabels:
      app: etcd
  template:
    metadata:
      labels:
        app: etcd
    spec:
      serviceAccountName: etcd-manager
      containers:
        - name: etcd
          image: registry/etcd:{{.EtcdVersion}}
          ports:
            - containerPort: 2379
            - containerPort: 2380
```

**类型 3：Helm Chart 组件**

```txt
bke-manifests/
└── monitoring/
    └── v2.0.0/
        ├── component.yaml          # 元数据
        ├── Chart.yaml              # Helm Chart 元数据
        ├── values.yaml             # 默认 values
        └── templates/
            ├── deployment.yaml
            ├── service.yaml
            └── configmap.yaml
```

```yaml
# component.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: monitoring-v2.0.0
spec:
  name: monitoring
  type: helm                        # 声明为 helm 类型
  version: v2.0.0
  dependencies: []
  upgradeStrategy:
    mode: Batch
    batchSize: 1
    timeout: "15m"
    failurePolicy: Continue
```

```yaml
# Chart.yaml
apiVersion: v2
name: monitoring
description: Monitoring stack for openFuyao
type: application
version: 2.0.0
appVersion: "2.0.0"
repository: https://charts.openfuyao.cn
```

```yaml
# values.yaml
replicaCount: {{.ControlPlaneReplicas}}

image:
  repository: registry/monitoring
  tag: {{.ProviderVersion}}

service:
  type: ClusterIP
  port: 9090

resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 250m
    memory: 256Mi

config:
  clusterName: {{.ClusterName}}
  region: {{.Region}}
```

**类型 4：二进制安装包组件**

```txt
bke-manifests/
└── containerd/
    └── v1.7.0/
        ├── component.yaml          # 元数据
        ├── binary.yaml             # 二进制包元数据
        ├── containerd-1.7.0-linux-amd64.tar.gz  # 二进制压缩包
        └── install.sh              # 安装脚本
```

```yaml
# component.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.0
spec:
  name: containerd
  type: binary                      # 声明为 binary 类型
  version: v1.7.0
  dependencies: []
  upgradeStrategy:
    mode: Batch
    batchSize: 1
    timeout: "20m"
    failurePolicy: FailFast
```

```yaml
# binary.yaml
name: containerd
version: v1.7.0
checksum: "sha256:abc123def456..."
downloadURL: "https://github.com/containerd/containerd/releases/download/v1.7.0/containerd-1.7.0-linux-amd64.tar.gz"
arch: amd64
os: linux
```

```bash
#!/bin/bash
# install.sh
set -e

COMPONENT_NAME="{{.Name}}"
VERSION="{{.Version}}"
INSTALL_DIR="/usr/local/bin"
TARBALL="containerd-1.7.0-linux-amd64.tar.gz"

echo "Installing $COMPONENT_NAME $VERSION..."

# 解压二进制包
tar -xzf $TARBALL -C /usr/local/

# 创建 systemd 服务
cat > /etc/systemd/system/containerd.service << EOF
[Unit]
Description=containerd container runtime
Documentation=https://containerd.io
After=network.target local-fs.target

[Service]
ExecStartPre=-/sbin/modprobe overlay
ExecStart=/usr/local/bin/containerd
Type=notify
Delegate=yes
KillMode=process
Restart=always
RestartSec=5
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity

[Install]
WantedBy=multi-user.target
EOF

# 启动服务
systemctl daemon-reload
systemctl enable containerd
systemctl restart containerd

echo "Installation completed successfully"
```

#### 4.5.2.2 TemplateContext 模板渲染模块

**设计思路**：组件 YAML 文件中可使用 Go template 语法声明变量占位符，在加载时由 `TemplateContext` 注入实际值。支持集群上下文、网络配置、节点信息等动态渲染。

**支持的变量类别**：

| 变量类别 | 示例变量 | 说明 |
| -------- | -------- | ---- |
| **集群上下文** | `{{.ClusterName}}`、`{{.Namespace}}`、`{{.ClusterUID}}` | 集群标识信息 |
| **版本信息** | `{{.KubernetesVersion}}`、`{{.EtcdVersion}}`、`{{.ProviderVersion}}` | 目标组件版本 |
| **网络配置** | `{{.PodCIDR}}`、`{{.ServiceCIDR}}`、`{{.DNSDomain}}` | 集群网络参数 |
| **节点信息** | `{{.ControlPlaneReplicas}}`、`{{.WorkerNodeCount}}`、`{{.NodeArch}}` | 节点规模与架构 |
| **环境配置** | `{{.Region}}`、`{{.AvailabilityZone}}`、`{{.Environment}}` | 部署环境信息 |
| **自定义变量** | `{{.Custom.*}}` | 用户通过 Annotation 传入的自定义变量 |

**ComponentVersion YAML 模板示例**：

```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: kubernetes-{{.KubernetesVersion}}
spec:
  name: kubernetes
  version: {{.KubernetesVersion}}
  inline:
    handler: EnsureKubernetesUpgrade
    version: v1.0
  resources:
    - kind: ConfigMap
      apiVersion: v1
      namespace: {{.Namespace}}
      name: k8s-config-{{.ClusterName}}
      data:
        cluster-uid: "{{.ClusterUID}}"
        region: "{{.Region}}"
        pod-cidr: "{{.PodCIDR}}"
        service-cidr: "{{.ServiceCIDR}}"
```

**TemplateContext 数据结构**：

```go
// pkg/manifest/template.go
type TemplateContext struct {
    ClusterName          string            `json:"clusterName"`
    Namespace            string            `json:"namespace"`
    ClusterUID           string            `json:"clusterUID"`
    KubernetesVersion    string            `json:"kubernetesVersion"`
    EtcdVersion          string            `json:"etcdVersion"`
    ProviderVersion      string            `json:"providerVersion"`
    PodCIDR              string            `json:"podCIDR"`
    ServiceCIDR          string            `json:"serviceCIDR"`
    DNSDomain            string            `json:"dnsDomain"`
    ControlPlaneReplicas int               `json:"controlPlaneReplicas"`
    WorkerNodeCount      int               `json:"workerNodeCount"`
    NodeArch             string            `json:"nodeArch"`
    Region               string            `json:"region"`
    AvailabilityZone     string            `json:"availabilityZone"`
    Environment          string            `json:"environment"`
    Custom               map[string]string `json:"custom,omitempty"`
}

// BuildTemplateContext 从 BKECluster 实例构建 TemplateContext
func BuildTemplateContext(bc *bkev1beta1.BKECluster) *TemplateContext {
    return &TemplateContext{
        ClusterName:          bc.Name,
        Namespace:            bc.Namespace,
        ClusterUID:           string(bc.UID),
        KubernetesVersion:    bc.Spec.KubernetesVersion,
        EtcdVersion:          bc.Spec.EtcdVersion,
        ProviderVersion:      bc.Status.ProviderVersion,
        PodCIDR:              bc.Spec.Networking.PodCIDR,
        ServiceCIDR:          bc.Spec.Networking.ServiceCIDR,
        DNSDomain:            bc.Spec.Networking.DNSDomain,
        ControlPlaneReplicas: bc.Spec.ControlPlane.Replicas,
        WorkerNodeCount:      bc.Spec.WorkerNodeCount,
        NodeArch:             bc.Spec.NodeArchitecture,
        Region:               bc.Spec.Region,
        AvailabilityZone:     bc.Spec.AvailabilityZone,
        Environment:          bc.Labels["environment"],
        Custom:               extractCustomAnnotations(bc),
    }
}

// extractCustomAnnotations 从 BKECluster 注解中提取自定义变量
func extractCustomAnnotations(bc *bkev1beta1.BKECluster) map[string]string {
    custom := make(map[string]string)
    for k, v := range bc.Annotations {
        if strings.HasPrefix(k, "cvo.openfuyao.cn/custom-") {
            key := strings.TrimPrefix(k, "cvo.openfuyao.cn/custom-")
            custom[key] = v
        }
    }
    return custom
}
```

**模板渲染核心方法实现**（位于 `pkg/manifest/store.go`）：

```go
// renderTemplate 渲染 ComponentVersion 元数据模板
func (s *ManifestStore) renderTemplate(cv *ComponentVersion, ctx *TemplateContext) (*ComponentVersion, error) {
    data, err := yaml.Marshal(cv)
    if err != nil {
        return nil, err
    }
    
    renderedData, err := s.renderYAMLContent(data, ctx)
    if err != nil {
        return nil, err
    }
    
    rendered := &ComponentVersion{}
    if err := yaml.Unmarshal(renderedData, rendered); err != nil {
        return nil, fmt.Errorf("failed to unmarshal rendered template: %w", err)
    }
    
    return rendered, nil
}

// renderYAMLContent 渲染任意 YAML 内容模板（通用方法）
func (s *ManifestStore) renderYAMLContent(data []byte, ctx *TemplateContext) ([]byte, error) {
    if ctx == nil || len(data) == 0 {
        return data, nil
    }
    
    tmpl, err := template.New("manifest").Funcs(template.FuncMap{
        "default": func(val, def string) string {
            if val == "" { return def }
            return val
        },
        "lower": strings.ToLower,
        "upper": strings.ToUpper,
    }).Parse(string(data))
    if err != nil {
        return nil, fmt.Errorf("failed to parse template: %w", err)
    }
    
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, ctx); err != nil {
        return nil, fmt.Errorf("failed to render template: %w", err)
    }
    
    return buf.Bytes(), nil
}
```

**TemplateContext 在 BKECluster 控制器中的调用位置**：

```txt
┌─────────────────────────────────────────────────────────────────┐
│ BKEClusterReconciler.Reconcile()                                │
├─────────────────────────────────────────────────────────────────┤
│ 1. 获取 BKECluster 实例                                          │
│ 2. 检查 ClusterVersion Annotation (upgrade-ready)               │
│    └─ 若无升级请求 → 返回                                        │
│ 3. 构建 VersionContext (版本比对上下文)                          │
│ 4. 构建 TemplateContext (模板渲染上下文) ◀──── 调用位置 1        │
│    └─ tmplCtx := manifest.BuildTemplateContext(bc)               │
│ 5. 判断 Install/Upgrade 场景                                     │
│ 6. 解析 ReleaseImage 获取组件列表                                │
│ 7. 构建 DAG (依赖图)                                            │
│ 8. 执行 DAG ◀──────────────────────────────────────────────────┤
│    └─ executeDAG(ctx, bc, dag, tmplCtx, vCtx)                   │
│       │                                                         │
│       ├─ 遍历 DAG 拓扑批次                                       │
│       ├─ 对每个组件:                                            │
│       │   ├─ GetComponentManifests(name, ver, tmplCtx) ◀─ 调用位置 2 │
│       │   │   └─ 内部根据 pkg.Type 加载对应资源                  │
│       │   │       └─ 渲染模板 (若传入 tmplCtx)                   │
│       │   └─ installerRegistry.InstallComponent(ctx, pkg, vCtx) │
│       │       └─ 根据 pkg.Type 路由到对应安装器                  │
│       └─ 返回执行结果                                           │
│ 9. 更新 ClusterVersion Status                                   │
└─────────────────────────────────────────────────────────────────┘
```

**调用链路代码示例**：

```go
// controllers/bkecluster_controller.go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    bc := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, bc); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 检查是否有升级请求
    if !r.isUpgradeReady(bc) {
        return ctrl.Result{}, nil
    }
    
    // 构建版本上下文
    vCtx := r.buildVersionContext(bc)
    
    // 构建模板渲染上下文 ◀──── 调用位置 1
    tmplCtx := manifest.BuildTemplateContext(bc)
    
    // 解析 ReleaseImage 获取组件列表
    ri, err := r.manifestStore.GetReleaseImage(bc.Spec.TargetVersion)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 构建 DAG
    dag := r.buildDAG(ri, vCtx)
    
    // 执行 DAG (内部会调用模板渲染和统一安装器)
    if err := r.executeDAG(ctx, bc, dag, tmplCtx, vCtx); err != nil {
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}

// executeDAG 执行 DAG 调度
func (r *BKEClusterReconciler) executeDAG(ctx context.Context, bc *bkev1beta1.BKECluster, dag *topology.DAG, tmplCtx *manifest.TemplateContext, vCtx *VersionContext) error {
    for _, batch := range dag.TopologicalSort() {
        var errs []error
        for _, compName := range batch {
            compRef := dag.GetComponent(compName)
            
            // 获取组件完整包（内部会根据 Type 加载对应资源并渲染模板）◀──── 调用位置 2
            pkg, err := r.manifestStore.GetComponentManifests(compName, compRef.Version, tmplCtx)
            if err != nil {
                errs = append(errs, err)
                continue
            }
            
            // 通过 InstallerRegistry 统一安装（自动根据 pkg.Type 路由到对应安装器）
            if err := r.installerRegistry.InstallComponent(ctx, pkg, vCtx); err != nil {
                errs = append(errs, fmt.Errorf("%s install (%s): %w", compName, pkg.Type, err))
                if compRef.Strategy.FailurePolicy == "FailFast" {
                    return kerrors.NewAggregate(errs)
                }
            }
        }
        if len(errs) > 0 { return kerrors.NewAggregate(errs) }
    }
    return nil
}
```

**模板渲染在整体流程中的位置**：

```txt
┌─────────────────────────────────────────────────────────┐
│         ManifestStore.GetComponentManifests()           │
├─────────────────────────────────────────────────────────┤
│ 1. 检查内存缓存 (sync.Map)                               │
│    ├─ Hit → 克隆 ComponentPackage                        │
│    └─ Miss → 继续                                       │
│                                                         │
│ 2. 尝试本地文件系统 / OCI 远程拉取                        │
│    ├─ 第一遍扫描: 加载 component.yaml                    │
│    │   └─ 获取 pkg.Type = cv.Spec.Type                  │
│    ├─ 第二遍扫描: 根据 Type 加载对应资源                 │
│    │   ├─ yaml    → 加载 *.yaml 文件 → Manifests        │
│    │   ├─ helm    → 加载 Chart/* 文件 → HelmChart       │
│    │   ├─ binary  → 加载 binary/* 文件 → BinaryArtifact │
│    │   └─ inline  → 无需额外文件 → InlineSpec           │
│    └─ 写入缓存 → 继续                                   │
│                                                         │
│ 3. 模板渲染 (若传入 TemplateContext)                     │
│    ├─ renderPackage(pkg, ctx)                           │
│    │   ├─ renderTemplate(cv, ctx) → 渲染元数据           │
│    │   └─ 根据 Type 渲染对应资源:                        │
│    │       ├─ yaml    → renderYAMLContent() 渲染清单    │
│    │       ├─ helm    → 渲染 values.yaml 模板            │
│    │       ├─ binary  → 渲染 install.sh 脚本模板         │
│    │       └─ inline  → 无需渲染 (Phase 代码处理)        │
│    └─ 返回渲染后的 ComponentPackage                      │
└─────────────────────────────────────────────────────────┘
```

#### 4.5.3 UpgradePath 图模型与路径查找算法

**设计思路**：UpgradePath 本质是一个**有向图 (Directed Graph)**，其中：

- **节点 (Vertex)**：版本号（如 v2.4.0, v2.5.0, v2.6.0）
- **边 (Edge)**：升级路径 `UpgradePathEdge`，携带 blocked/deprecated/preCheck 等属性
- **路径查找**：在图中寻找从 `from` 到 `to` 的可达路径，优先选择最短路径

**图模型设计**：

```txt
                    UpgradePath Graph
         ┌──────────────────────────────────┐
         │                                  │
    ┌────▼────┐      ┌────▼────┐      ┌────▼────┐
    │ v2.4.0  │────▶│ v2.5.0  │─────▶│ v2.6.0  │
    │         │  edge│         │  edge│         │
    └─────────┘      └─────────┘      └─────────┘
         │                                ▲
         │                                │
         │          ┌─────────────┐       │
         └────────▶│  (blocked)  │───────┘
                    └─────────────┘
              v2.4.0 → v2.6.0 direct edge (blocked)
```

**路径查找算法 (BFS 最短路径)**：

```txt
┌─────────────────────────────────────────────────────────┐
│ FindUpgradePath(from, to)                               │
├─────────────────────────────────────────────────────────┤
│ 1. 初始化                                                │
│    ├─ queue ← [(from, [from])]                          │
│    ├─ visited ← {from}                                  │
│    └─ parentMap ← map[string]string                     │
│                                                         │
│ 2. BFS 遍历                                              │
│    while queue not empty:                               │
│      ├─ curr, path ← dequeue(queue)                     │
│      ├─ if curr == to: return path (找到最短路径)        │
│      ├─ for each edge in graph.GetEdgesFrom(curr):      │
│        ├─ if edge.Blocked: continue                     │
│        ├─ nextVer := edge.To                            │
│        ├─ if nextVer in visited: continue               │
│        ├─ visited.Add(nextVer)                          │
│        ├─ parentMap[nextVer] = curr                     │
│        └─ enqueue(queue, (nextVer, path + [nextVer]))   │
│                                                         │
│ 3. 路径重建                                              │
│    └─ 从 to 沿 parentMap 回溯到 from，反转得到完整路径   │
│                                                         │
│ 4. 未找到                                                │
│    └─ return nil, "no valid upgrade path"               │
└─────────────────────────────────────────────────────────┘
```

**代码实现**：

```go
// pkg/upgrade/graph.go

// UpgradePathGraph 升级路径图
type UpgradePathGraph struct {
    mu      sync.RWMutex
    adj     map[string][]UpgradePathEdge // 邻接表: version -> edges
    digest  string                       // 当前 OCI digest
}

func NewUpgradePathGraph() *UpgradePathGraph {
    return &UpgradePathGraph{
        adj: make(map[string][]UpgradePathEdge),
    }
}

// LoadFromEdges 从边列表构建图
func (g *UpgradePathGraph) LoadFromEdges(edges []UpgradePathEdge, digest string) {
    g.mu.Lock()
    defer g.mu.Unlock()
    
    g.adj = make(map[string][]UpgradePathEdge)
    for _, edge := range edges {
        g.adj[edge.From] = append(g.adj[edge.From], edge)
        // 确保目标节点也存在
        if _, ok := g.adj[edge.To]; !ok {
            g.adj[edge.To] = []UpgradePathEdge{}
        }
    }
    g.digest = digest
}

// FindPath BFS 查找最短升级路径
func (g *UpgradePathGraph) FindPath(from, to string) ([]UpgradePathEdge, error) {
    g.mu.RLock()
    defer g.mu.RUnlock()
    
    if from == to {
        return nil, nil
    }
    
    // BFS
    type queueItem struct {
        version string
        path    []UpgradePathEdge
    }
    
    queue := []queueItem{{version: from, path: []UpgradePathEdge{}}}
    visited := map[string]bool{from: true}
    
    for len(queue) > 0 {
        curr := queue[0]
        queue = queue[1:]
        
        if curr.version == to {
            return curr.path, nil
        }
        
        for _, edge := range g.adj[curr.version] {
            if edge.Blocked || edge.Deprecated {
                continue
            }
            if visited[edge.To] {
                continue
            }
            visited[edge.To] = true
            newPath := append(append([]UpgradePathEdge{}, curr.path...), edge)
            queue = append(queue, queueItem{version: edge.To, path: newPath})
        }
    }
    
    return nil, fmt.Errorf("no valid upgrade path from %s to %s", from, to)
}

// GetAllVersions 获取图中所有版本节点
func (g *UpgradePathGraph) GetAllVersions() []string {
    g.mu.RLock()
    defer g.mu.RUnlock()
    
    versions := make([]string, 0, len(g.adj))
    for v := range g.adj {
        versions = append(versions, v)
    }
    return versions
}

// HasVersion 检查版本是否存在于图中
func (g *UpgradePathGraph) HasVersion(ver string) bool {
    g.mu.RLock()
    defer g.mu.RUnlock()
    _, ok := g.adj[ver]
    return ok
}

// DetectCycle 检测图中是否存在环 (DFS)
func (g *UpgradePathGraph) DetectCycle() error {
    g.mu.RLock()
    defer g.mu.RUnlock()
    
    visited := make(map[string]bool)
    recStack := make(map[string]bool)
    
    var dfs func(node string) bool
    dfs = func(node string) bool {
        visited[node] = true
        recStack[node] = true
        
        for _, edge := range g.adj[node] {
            if !visited[edge.To] {
                if dfs(edge.To) {
                    return true
                }
            } else if recStack[edge.To] {
                return true
            }
        }
        
        recStack[node] = false
        return false
    }
    
    for node := range g.adj {
        if !visited[node] {
            if dfs(node) {
                return fmt.Errorf("cycle detected in upgrade path graph")
            }
        }
    }
    return nil
}
```

### 4.6 控制器架构与逻辑

#### 4.6.1 控制器概览

| 控制器 | 核心职责 | 协同方式 |
| ---- | ---- | ------ |
| **ClusterVersionReconciler** | 版本声明管理、ReleaseImage/UpgradePath OCI 拉取与解析、触发 BKECluster 调谐 | 更新 BKECluster Annotation `cvo.openfuyao.cn/upgrade-ready` |
| **ReleaseImageReconciler** | 清单校验、兼容性矩阵验证、bke-manifests 路径验证、状态标记 | 独立调谐，更新 `Status.Phase` |
| **UpgradePathReconciler** | 路径图构建、Digest 监控、环检测、提供 `FindPath()` 接口 | 维护 UpgradePathGraph，供 CV 调用 |
| **BKEClusterReconciler** (增强) | 监听版本变更、构建 VersionContext、预创建资源、DAG 调度、Phase 桥接、状态回写 | 直接 Watch CV；从 ComponentFactory 获取 Phase；更新 CV Status |

#### 4.6.2 ClusterVersionReconciler 详细设计

**职责**：

- 监听 `ClusterVersion` 资源变更
- 根据 `Spec.DesiredVersion` 拉取并解析对应的 `ReleaseImage` 和 `UpgradePath`
- 执行升级路径合法性校验
- 触发 `BKECluster` 调谐流程

**调谐流程**：

```txt
┌─────────────────────────────────────────────────────────────┐
│ ClusterVersionReconciler.Reconcile()                        │
├─────────────────────────────────────────────────────────────┤
│ 1. 获取 ClusterVersion 实例                                  │
│ 2. 检查是否已关联 ReleaseImage                               │
│    ├─ 未关联 → 调用 manifestStore.GetReleaseImage()         │
│    │         → 创建/更新 ReleaseImage 引用                   │
│    └─ 已关联 → 验证引用有效性                                │
│ 3. 获取 UpgradePath (图查找)                                 │
│    ├─ 调用 upgradePathGraph.FindPath(current, target)       │
│    ├─ BFS 查找最短路径 → 返回路径边列表                      │
│    └─ 检查路径中是否有 blocked 边 → 若拦截则标记 Blocked     │
│ 4. 执行预检                                                  │
│    ├─ 兼容性校验 (调用 ReleaseImageReconciler 的验证逻辑)    │
│    └─ 失败 → 标记 Status.Phase=PreCheckFailed               │
│ 5. 触发 BKECluster 调谐                                      │
│    └─ 写入 Annotation: cvo.openfuyao.cn/upgrade-ready       │
│ 6. 更新 Status                                               │
│    └─ CurrentVersion, DesiredVersion, Phase                 │
└─────────────────────────────────────────────────────────────┘
```

**核心代码**：

```go
func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &cvoapi.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 1. 解析 ReleaseImage
    if cv.Spec.ReleaseImageRef == "" {
        ri, err := r.manifestStore.GetReleaseImage(cv.Spec.DesiredVersion)
        if err != nil {
            return r.updateStatus(ctx, cv, cvoapi.PhaseFailed, err.Error())
        }
        cv.Spec.ReleaseImageRef = ri.Name
    }

    // 2. 验证升级路径 (图查找)
    pathEdges, err := r.upgradePathStore.FindPath(cv.Status.CurrentVersion, cv.Spec.DesiredVersion)
    if err != nil {
        return r.updateStatus(ctx, cv, cvoapi.PhaseBlocked, "no valid upgrade path")
    }
    
    // 检查路径中是否有被拦截的边
    for _, edge := range pathEdges {
        if edge.Blocked {
            return r.updateStatus(ctx, cv, cvoapi.PhaseBlocked, fmt.Sprintf("upgrade path blocked at %s", edge.From))
        }
    }

    // 3. 触发 BKECluster 调谐
    bc := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, cv.OwnerRef, bc); err != nil {
        return ctrl.Result{}, err
    }
    if bc.Annotations == nil {
        bc.Annotations = make(map[string]string)
    }
    bc.Annotations["cvo.openfuyao.cn/upgrade-ready"] = cv.Spec.DesiredVersion
    r.Update(ctx, bc)

    return r.updateStatus(ctx, cv, cvoapi.PhaseReady, "")
}
```

#### 4.6.2.1 多跳升级路径调谐流程设计

**场景说明**：当集群从 v2.4.0 升级到 v2.7.0 时，UpgradePath 图可能返回多跳路径：`v2.4.0 → v2.5.0 → v2.6.0 → v2.7.0`。ClusterVersionReconciler 需要逐跳执行升级，每跳完成后更新 `CurrentVersion` 并触发下一跳。

**多跳升级策略**：

| 策略 | 说明 | 适用场景 |
| ---- | ---- | -------- |
| **自动连续升级** | 一次性执行所有跳，中间不暂停 | 测试环境、小版本连续升级 |
| **逐跳确认升级** | 每跳完成后暂停，等待用户确认或自动检查点通过 | 生产环境、跨大版本升级 |
| **可回滚升级** | 每跳创建检查点，失败时回滚到上一跳起点 | 关键业务集群 |

**调谐流程设计**：

```txt
┌─────────────────────────────────────────────────────────────────┐
│ ClusterVersionReconciler 多跳升级调谐流程                        │
├─────────────────────────────────────────────────────────────────┤
│ 1. 获取 ClusterVersion 实例                                      │
│ 2. 计算升级路径                                                  │
│    ├─ pathEdges = upgradePathGraph.FindPath(current, target)    │
│    └─ 例: [v2.4.0→v2.5.0, v2.5.0→v2.6.0, v2.6.0→v2.7.0]       │
│ 3. 确定当前应执行的跳                                            │
│    ├─ 读取 cv.Status.UpgradeProgress.CurrentHopIndex            │
│    ├─ 若为 0 或未设置 → 从第一跳开始                             │
│    └─ 若已完成 N 跳 → 从第 N+1 跳继续                            │
│ 4. 执行当前跳升级                                                │
│    ├─ currentHop = pathEdges[CurrentHopIndex]                   │
│    ├─ hopTarget = currentHop.To                                 │
│    ├─ 获取 hopTarget 对应的 ReleaseImage                        │
│    ├─ 执行兼容性校验                                            │
│    └─ 写入 BKECluster Annotation: upgrade-ready=hopTarget       │
│ 5. 等待 BKECluster 完成当前跳升级                                │
│    ├─ 监听 BKECluster.Status.Version = hopTarget                │
│    ├─ 或监听 Annotation: upgrade-completed=hopTarget            │
│    └─ 超时 → 标记当前跳失败，暂停升级                            │
│ 6. 当前跳完成处理                                                │
│    ├─ 更新 cv.Status.CurrentVersion = hopTarget                 │
│    ├─ cv.Status.UpgradeProgress.CurrentHopIndex++               │
│    ├─ 记录已完成跳到 cv.Status.UpgradeProgress.CompletedHops    │
│    └─ 触发下一次 Reconcile (继续下一跳)                          │
│ 7. 所有跳完成                                                    │
│    ├─ cv.Status.Phase = Upgraded                                │
│    ├─ cv.Status.UpgradeProgress.Completed = true                │
│    └─ 清除 BKECluster upgrade-ready Annotation                  │
└─────────────────────────────────────────────────────────────────┘
```

**核心数据结构**：

```go
// ClusterVersionStatus 升级进度追踪
type UpgradeProgress struct {
    CurrentHopIndex  int              `json:"currentHopIndex"`   // 当前应执行的跳索引 (0-based)
    TotalHops        int              `json:"totalHops"`         // 总跳数
    PathEdges        []UpgradePathEdge `json:"pathEdges"`        // 完整路径边列表
    CompletedHops    []CompletedHop   `json:"completedHops"`     // 已完成的跳
    StartedAt        *metav1.Time     `json:"startedAt"`
    CompletedAt      *metav1.Time     `json:"completedAt,omitempty"`
}

type CompletedHop struct {
    From        string         `json:"from"`
    To          string         `json:"to"`
    StartedAt   metav1.Time    `json:"startedAt"`
    CompletedAt metav1.Time    `json:"completedAt"`
    Status      HopStatus      `json:"status"` // Succeeded, Failed, RolledBack
}

type HopStatus string

const (
    HopSucceeded HopStatus = "Succeeded"
    HopFailed    HopStatus = "Failed"
    HopRolledBack HopStatus = "RolledBack"
)
```

**核心代码实现**：

```go
func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &cvoapi.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 1. 解析 ReleaseImage
    if cv.Spec.ReleaseImageRef == "" {
        ri, err := r.manifestStore.GetReleaseImage(cv.Spec.DesiredVersion)
        if err != nil {
            return r.updateStatus(ctx, cv, cvoapi.PhaseFailed, err.Error())
        }
        cv.Spec.ReleaseImageRef = ri.Name
    }

    // 2. 计算升级路径
    pathEdges, err := r.upgradePathGraph.FindPath(cv.Status.CurrentVersion, cv.Spec.DesiredVersion)
    if err != nil {
        return r.updateStatus(ctx, cv, cvoapi.PhaseBlocked, "no valid upgrade path")
    }
    
    // 检查路径中是否有被拦截的边
    for _, edge := range pathEdges {
        if edge.Blocked {
            return r.updateStatus(ctx, cv, cvoapi.PhaseBlocked, fmt.Sprintf("upgrade path blocked at %s", edge.From))
        }
    }

    // 3. 多跳升级逻辑
    if len(pathEdges) > 1 {
        return r.reconcileMultiHop(ctx, cv, pathEdges)
    }

    // 4. 单跳升级（原有逻辑）
    bc := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, cv.OwnerRef, bc); err != nil {
        return ctrl.Result{}, err
    }
    if bc.Annotations == nil {
        bc.Annotations = make(map[string]string)
    }
    bc.Annotations["cvo.openfuyao.cn/upgrade-ready"] = cv.Spec.DesiredVersion
    r.Update(ctx, bc)

    return r.updateStatus(ctx, cv, cvoapi.PhaseReady, "")
}

// reconcileMultiHop 处理多跳升级
func (r *ClusterVersionReconciler) reconcileMultiHop(ctx context.Context, cv *cvoapi.ClusterVersion, pathEdges []UpgradePathEdge) (ctrl.Result, error) {
    // 初始化或获取升级进度
    if cv.Status.UpgradeProgress == nil {
        cv.Status.UpgradeProgress = &UpgradeProgress{
            CurrentHopIndex: 0,
            TotalHops:       len(pathEdges),
            PathEdges:       pathEdges,
            StartedAt:       &metav1.Time{Time: time.Now()},
        }
    }

    progress := cv.Status.UpgradeProgress
    
    // 检查是否所有跳已完成
    if progress.CurrentHopIndex >= progress.TotalHops {
        cv.Status.Phase = cvoapi.PhaseUpgraded
        cv.Status.UpgradeProgress.CompletedAt = &metav1.Time{Time: time.Now()}
        // 清除 BKECluster Annotation
        r.clearUpgradeAnnotation(ctx, cv)
        return r.updateStatus(ctx, cv, cvoapi.PhaseUpgraded, "multi-hop upgrade completed")
    }

    // 获取当前应执行的跳
    currentHop := pathEdges[progress.CurrentHopIndex]
    hopTarget := currentHop.To

    // 检查当前跳是否已完成（通过 BKECluster 状态判断）
    bc := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, cv.OwnerRef, bc); err != nil {
        return ctrl.Result{}, err
    }

    if bc.Status.Version == hopTarget || bc.Annotations["cvo.openfuyao.cn/upgrade-completed"] == hopTarget {
        // 当前跳已完成，更新进度并触发下一跳
        progress.CompletedHops = append(progress.CompletedHops, CompletedHop{
            From:        currentHop.From,
            To:          hopTarget,
            StartedAt:   metav1.Now(),
            CompletedAt: metav1.Now(),
            Status:      HopSucceeded,
        })
        progress.CurrentHopIndex++
        cv.Status.CurrentVersion = hopTarget
        
        // 触发下一次 Reconcile 继续下一跳
        return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 5}, r.updateStatus(ctx, cv, cvoapi.PhaseUpgrading, fmt.Sprintf("completed hop %d/%d, continuing to %s", progress.CurrentHopIndex, progress.TotalHops, hopTarget))
    }

    // 检查当前跳是否正在执行
    if bc.Annotations["cvo.openfuyao.cn/upgrade-ready"] == hopTarget {
        // 正在执行中，等待完成
        return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 10}, nil
    }

    // 触发当前跳升级
    if bc.Annotations == nil {
        bc.Annotations = make(map[string]string)
    }
    bc.Annotations["cvo.openfuyao.cn/upgrade-ready"] = hopTarget
    bc.Annotations["cvo.openfuyao.cn/upgrade-hop"] = fmt.Sprintf("%d/%d", progress.CurrentHopIndex+1, progress.TotalHops)
    r.Update(ctx, bc)

    return r.updateStatus(ctx, cv, cvoapi.PhaseUpgrading, fmt.Sprintf("executing hop %d/%d: %s → %s", progress.CurrentHopIndex+1, progress.TotalHops, currentHop.From, hopTarget))
}
```

#### 4.6.3 ReleaseImageReconciler 详细设计

**职责**：

- 监听 `ReleaseImage` 资源变更
- 验证 OCI 镜像完整性与签名
- 解析并校验 `bke-manifests` 中所有组件清单
- 执行兼容性矩阵验证
- 更新 `Status.Phase` 标记校验结果

**调谐流程**：

```txt
┌─────────────────────────────────────────────────────────────┐
│ ReleaseImageReconciler.Reconcile()                          │
├─────────────────────────────────────────────────────────────┤
│ 1. 获取 ReleaseImage 实例                                    │
│ 2. 验证 OCI 镜像                                             │
│    ├─ 拉取镜像 (若未缓存)                                    │
│    ├─ 验证 Cosign 签名                                       │
│    └─ 失败 → 标记 Status.Phase=Invalid                      │
│ 3. 解析组件清单                                              │
│    ├─ 遍历 spec.install.components 和 spec.upgrade.components│
│    ├─ 调用 manifestStore.GetComponentManifests() 获取组件包  │
│    │   └─ 传入 ctx=nil，仅获取元数据与清单，不渲染模板        │
│    └─ 缺失 → 标记 Status.Phase=ManifestMissing              │
│ 4. 兼容性校验                                                │
│    ├─ 扁平化所有组件 (展开 composite)                        │
│    ├─ 构建约束图                                             │
│    ├─ 调用 CheckCompatibility() 执行 CSP 求解               │
│    └─ 冲突 → 标记 Status.Phase=CompatibilityFailed          │
│ 5. 更新 Status                                               │
│    └─ Phase=Valid, ComponentCount, ValidatedAt              │
└─────────────────────────────────────────────────────────────┘
```

**核心代码**：

```go
func (r *ReleaseImageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    ri := &cvoapi.ReleaseImage{}
    if err := r.Get(ctx, req.NamespacedName, ri); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 1. 验证 OCI 签名
    if err := r.verifyOCISignature(ctx, ri); err != nil {
        return r.updateStatus(ctx, ri, cvoapi.PhaseInvalid, err.Error())
    }

    // 2. 解析并验证所有组件
    var components []ComponentRef
    for _, comp := range ri.Spec.Install.Components {
        components = append(components, ComponentRef{Name: comp.Name, Version: comp.Version})
    }
    for _, comp := range ri.Spec.Upgrade.Components {
        components = append(components, ComponentRef{Name: comp.Name, Version: comp.Version})
    }

    // 3. 获取组件包并验证清单完整性 (ctx=nil 不渲染模板)
    for _, comp := range components {
        pkg, err := r.manifestStore.GetComponentManifests(comp.Name, comp.Version, nil)
        if err != nil {
            return r.updateStatus(ctx, ri, cvoapi.PhaseManifestMissing, 
                fmt.Sprintf("component %s@%s not found: %v", comp.Name, comp.Version, err))
        }
        if pkg.Metadata == nil {
            return r.updateStatus(ctx, ri, cvoapi.PhaseManifestMissing,
                fmt.Sprintf("component %s@%s missing component.yaml", comp.Name, comp.Version))
        }
    }

    // 4. 扁平化 composite 组件
    flatList, err := r.flattenComponents(components)
    if err != nil {
        return r.updateStatus(ctx, ri, cvoapi.PhaseInvalid, err.Error())
    }

    // 5. 兼容性校验
    if err := CheckCompatibility(flatList, r.manifestStore); err != nil {
        return r.updateStatus(ctx, ri, cvoapi.PhaseCompatibilityFailed, err.Error())
    }

    return r.updateStatus(ctx, ri, cvoapi.PhaseValid, "")
}
```

#### 4.6.4 UpgradePathReconciler 详细设计

**职责**：

- 监听 `UpgradePath` 资源变更（单 CR 包含所有路径）
- 解析 OCI 镜像并监控 digest 变更
- 构建并维护升级路径图 (UpgradePathGraph)
- 提供路径查询接口供其他控制器调用

**调谐流程**：

```txt
┌─────────────────────────────────────────────────────────────┐
│ UpgradePathReconciler.Reconcile()                           │
├─────────────────────────────────────────────────────────────┤
│ 1. 获取 UpgradePath CR (单 CR)                               │
│ 2. 验证路径规则                                              │
│    ├─ 检查所有 edges 的 from/to 版本格式 (semver 合规)       │
│    ├─ 构建临时图 → 执行环检测                                │
│    └─ 失败 → 标记 Status.Phase=Invalid                      │
│ 3. 更新路径图索引                                            │
│    └─ graph.LoadFromEdges(cr.Spec.Paths, cr.Status.Digest)  │
│ 4. 更新 Status                                              │
│    └─ Phase=Active, PathCount, LastDigest                   │
└─────────────────────────────────────────────────────────────┘
```

**核心代码**：

```go
func (r *UpgradePathReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    up := &cvoapi.UpgradePath{}
    if err := r.Get(ctx, req.NamespacedName, up); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 1. 验证所有路径边
    for _, edge := range up.Spec.Paths {
        if _, err := semver.NewVersion(edge.From); err != nil {
            return r.updateStatus(ctx, up, cvoapi.PhaseInvalid, "invalid from version")
        }
        if _, err := semver.NewVersion(edge.To); err != nil {
            return r.updateStatus(ctx, up, cvoapi.PhaseInvalid, "invalid to version")
        }
    }

    // 2. 构建图并检测环
    tempGraph := NewUpgradePathGraph()
    tempGraph.LoadFromEdges(up.Spec.Paths, "")
    if err := tempGraph.DetectCycle(); err != nil {
        return r.updateStatus(ctx, up, cvoapi.PhaseInvalid, err.Error())
    }

    // 3. 更新全局图索引
    r.graph.LoadFromEdges(up.Spec.Paths, up.Status.LastDigest)

    return r.updateStatus(ctx, up, cvoapi.PhaseActive, "")
}

func (r *UpgradePathReconciler) FindPath(from, to string) ([]cvoapi.UpgradePathEdge, error) {
    return r.graph.FindPath(from, to)
}
```

#### 4.6.5 BKEClusterReconciler 增强设计

**核心代码片段 (DAG 调度与统一安装器调用)**：

```go
func (r *BKEClusterReconciler) executeDAG(ctx context.Context, bc *bkev1beta1.BKECluster, dag *topology.DAG, scenario string) error {
    vCtx := r.buildVersionContext(bc)
    tmplCtx := manifest.BuildTemplateContext(bc)
    
    for _, batch := range dag.TopologicalSort() {
        var errs []error
        for _, compName := range batch {
            compRef := dag.GetComponent(compName)
            
            // 从 ManifestStore 获取组件完整包（含 Type 字段标识组件类型）
            pkg, err := r.manifestStore.GetComponentManifests(compName, compRef.Version, tmplCtx)
            if err != nil {
                errs = append(errs, err)
                continue
            }
            
            // 通过 InstallerRegistry 统一安装（自动根据 pkg.Type 路由到对应安装器）
            if err := r.installerRegistry.InstallComponent(ctx, pkg, vCtx); err != nil {
                errs = append(errs, fmt.Errorf("%s install (%s): %w", compName, pkg.Type, err))
                if compRef.Strategy.FailurePolicy == "FailFast" { 
                    return kerrors.NewAggregate(errs) 
                }
            }
        }
        if len(errs) > 0 { return kerrors.NewAggregate(errs) }
    }
    return nil
}
```

**执行流程说明**：

1. `GetComponentManifests()` 返回的 `ComponentPackage` 包含 `Type` 字段，标识组件类型（yaml/helm/inline/binary）
2. `InstallerRegistry.InstallComponent()` 根据 `pkg.Type` 自动路由到对应安装器：
   - `yaml` → `YamlInstaller.Install()` → 调用 `ManifestApplier.ApplyManifests()`
   - `helm` → `HelmInstaller.Install()` → 调用 Helm SDK 安装/升级
   - `inline` → `InlineInstaller.Install()` → 调用 `ComponentFactory.Resolve()` 执行 Phase
   - `binary` → `BinaryInstaller.Install()` → 下载、解压、执行安装脚本
3. 所有安装器实现统一的 `Install/Uninstall/HealthCheck` 接口，支持扩展新类型

### 4.7 升级流程与 NeedExecute 复用设计

**严格不新增 `ShouldUpgrade()` 接口**。改造现有 Phase 的 `NeedExecute(old, new)`：

```go
func (p *EnsureEtcdUpgrade) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    if !p.BasePhase.DefaultNeedExecute(old, new) { return false }

    if featuregate.DeclarativeUpgradeEnabled(new) {
        ctx := p.GetVersionContext()
        if ctx == nil { return false }

        cur := ctx.Current["etcd"]
        tgt := ctx.Target["etcd"]
        if cur == tgt || tgt == "" {
            p.Log.V(4).Info("Component version unchanged, skipping")
            return false
        }
        p.Log.Info("Declarative Upgrade triggered: %s -> %s", cur, tgt)
        return true
    }
    return p.isEtcdNeedUpgrade(old, new)
}

// Version：返回组件版本
func (p *EnsureEtcdUpgrade) Version() string {
    return p.Ctx.BKECluster.Status.EtcdVersion
}
```

#### 4.7.1 **Feature Gate 与 Context 实现**

```go
// pkg/featuregate/features.go
var featureGate = featuregate.NewFeatureGate()

func init() {
    featureGate.Add(map[featuregate.Feature]featuregate.FeatureSpec{
        "DeclarativeUpgrade": {Default: false, PreRelease: featuregate.Alpha},
    })
}

func DeclarativeUpgradeEnabled(obj metav1.Object) bool {
    // 支持通过 Annotation 覆盖全局 FeatureGate
    if obj != nil {
        if v, ok := obj.GetAnnotations()["cvo.openfuyao.cn/declarative-upgrade"]; ok {
            return v == "true"
        }
    }
    return featureGate.Enabled("DeclarativeUpgrade")
}

// pkg/phaseframe/context.go
func (p *BasePhase) GetVersionContext() *VersionContext {
    // 从 PhaseContext 中获取注入的版本上下文
    if p.Ctx == nil { return nil }
    return p.Ctx.VersionContext
}
```

#### 4.7.2  VersionContext 数据结构与构建过程

```go
// pkg/upgrade/context.go
type VersionContext struct {
    mu      sync.RWMutex
    Current map[string]string // 组件名 -> 当前运行版本
    Target  map[string]string // 组件名 -> 目标期望版本
}

func NewVersionContext() *VersionContext {
    return &VersionContext{
        Current: make(map[string]string),
        Target:  make(map[string]string),
    }
}

func (vc *VersionContext) SetCurrent(name, ver string) {
    vc.mu.Lock(); defer vc.mu.Unlock()
    vc.Current[name] = ver
}
func (vc *VersionContext) SetTarget(name, ver string) {
    vc.mu.Lock(); defer vc.mu.Unlock()
    vc.Target[name] = ver
}
func (vc *VersionContext) GetTarget(name string) string {
    vc.mu.RLock(); defer vc.mu.RUnlock()
    return vc.Target[name]
}
```

**构建过程**：

```txt
┌─────────────────────────────────────────────────────────────┐
│ BKEClusterReconciler.buildVersionContext(bc)                │
├─────────────────────────────────────────────────────────────┤
│ 1. 获取 ClusterVersion                                      │
│    └─ cv := GetClusterVersionForBKECluster(bc)              │
│                                                             │
│ 2. 构建 Target 版本映射                                      │
│    ├─ desiredVer := cv.Spec.DesiredVersion                  │
│    ├─ targetRI := manifestStore.GetReleaseImage(desiredVer) │
│    ├─ for each component in targetRI.Spec.Upgrade:          │
│    │   vCtx.SetTarget(component.Name, component.Version)    │
│    └─ for each component in targetRI.Spec.Install:          │
│        vCtx.SetTarget(component.Name, component.Version)    │
│                                                             │
│ 3. 构建 Current 版本映射                                     │
│    ├─ currentVer := cv.Status.CurrentVersion                │
│    ├─ currentRI := manifestStore.GetReleaseImage(currentVer)│
│    ├─ for each component in currentRI.Spec.Upgrade:         │
│    │   vCtx.SetCurrent(component.Name, component.Version)   │
│    └─ for each component in currentRI.Spec.Install:         │
│        vCtx.SetCurrent(component.Name, component.Version)   │
│                                                             │
│ 4. 注入到 PhaseContext                                       │
│    └─ bc.PhaseContext.VersionContext = vCtx                 │
│                                                             │
│ 5. 返回 VersionContext                                       │
└─────────────────────────────────────────────────────────────┘
```

**流程图**：

```txt
Start
  │
  ▼
┌─────────────────────────────┐
│ 获取 ClusterVersion 实例     │
│ (通过 OwnerReference 关联)   │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ 读取 DesiredVersion         │
│ 调用 manifestStore          │
│ .GetReleaseImage(desired)   │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ 遍历 ReleaseImage           │
│ .Spec.Install.Components    │
│ .Spec.Upgrade.Components    │
│ 填充 VersionContext.Target   │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ 读取 CurrentVersion         │
│ 调用 manifestStore          │
│ .GetReleaseImage(current)   │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ 遍历 ReleaseImage           │
│ .Spec.Install.Components    │
│ .Spec.Upgrade.Components    │
│ 填充 VersionContext.Current │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ 将 VersionContext 注入到     │
│ BKECluster.PhaseContext     │
└──────────────┬──────────────┘
               │
               ▼
┌─────────────────────────────┐
│ 返回 VersionContext         │
│ 供 DAG 调度器使用            │
└─────────────────────────────┘
```

#### 4.7.3 升级前资源预创建扩展机制

**场景描述**：新版本引入的资源（如 ConfigMap、Secret、CRD 等）在旧版本中不存在，需要在升级过程中预先创建，否则后续 Phase 可能因依赖缺失而失败。

**设计思路**：将资源预创建设计为标准的 **inline Phase 组件**，在 `ReleaseImage` 的 `upgrade.components` 中声明，通过 DAG 依赖关系确保在其他组件之前执行。完全复用现有 `ComponentFactory` 和 `NeedExecute()` 机制，无需额外调度逻辑。

**架构设计**：

```txt
┌─────────────────────────────────────────────────────────┐
│              ReleaseImage 升级组件声明                    │
├─────────────────────────────────────────────────────────┤
│ upgrade:                                                 │
│   components:                                            │
│     - name: pre-upgrade-resources  ◀──── 新增组件        │
│       version: v1.0.0                                    │
│     - name: provider-upgrade                             │
│       version: v1.2.0                                    │
│     - name: etcd-upgrade                                 │
│       version: v3.5.12                                   │
│                                                          │
│  DAG 执行顺序:                                           │
│  pre-upgrade-resources → provider-upgrade → etcd-upgrade │
└─────────────────────────────────────────────────────────┘
```

**ComponentVersion 定义**：

```yaml
# bke-manifests/pre-upgrade-resources/v1.0.0/component.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: pre-upgrade-resources-v1.0.0
spec:
  name: pre-upgrade-resources
  type: leaf
  version: v1.0.0
  inline:
    handler: EnsurePreUpgradeResources
    version: v1.0
  dependencies: []  # 无依赖，确保最先执行
  upgradeStrategy:
    mode: Batch
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
  resources:
    - kind: ConfigMap
      apiVersion: v1
      namespace: kube-system
      name: bke-new-feature-config
      data:
        feature-flag: "enabled"
    - kind: Secret
      apiVersion: v1
      namespace: kube-system
      name: bke-new-cert
      stringData:
        tls.crt: "..."
        tls.key: "..."
```

**Go 结构体扩展**：

```go
// ComponentVersionSpec 扩展
type ComponentVersionSpec struct {
    Name            string              `json:"name"`
    Type            ComponentType       `json:"type"`
    Version         string              `json:"version"`
    Inline          *InlineSpec         `json:"inline,omitempty"`
    SubComponents   []SubComponent      `json:"subComponents,omitempty"`
    Compatibility   CompatibilitySpec   `json:"compatibility,omitempty"`
    Dependencies    []Dependency        `json:"dependencies,omitempty"`
    UpgradeStrategy UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
    Resources       []ResourceSpec      `json:"resources,omitempty"`  // 新增
}

// ResourceSpec 定义需要创建的资源
type ResourceSpec struct {
    Kind       string            `json:"kind"`
    APIVersion string            `json:"apiVersion"`
    Namespace  string            `json:"namespace,omitempty"`
    Name       string            `json:"name"`
    Labels     map[string]string `json:"labels,omitempty"`
    Data       map[string]string `json:"data,omitempty"`
    StringData map[string]string `json:"stringData,omitempty"`
    Manifest   string            `json:"manifest,omitempty"`
}
```

**Inline Handler 实现**：

```go
// pkg/phase/preupgraderesources/ensure.go
type EnsurePreUpgradeResources struct {
    *phaseframe.BasePhase
    client client.Client
}

func (p *EnsurePreUpgradeResources) Name() string {
    return "EnsurePreUpgradeResources"
}

func (p *EnsurePreUpgradeResources) Version() string {
    return "v1.0"
}

func (p *EnsurePreUpgradeResources) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    if !p.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }
    
    vCtx := p.GetVersionContext()
    if vCtx == nil {
        return false
    }
    
    // 仅当目标版本包含此组件时执行
    return vCtx.Target["pre-upgrade-resources"] != ""
}

func (p *EnsurePreUpgradeResources) Execute(ctx context.Context) error {
    // 从 ComponentVersion 获取资源清单
    resources := p.GetComponentVersion().Spec.Resources
    if len(resources) == 0 {
        p.Log.V(4).Info("No pre-upgrade resources defined, skipping")
        return nil
    }
    
    // 按依赖顺序排序 (CRD → ConfigMap → Secret → ...)
    sorted := p.sortResourcesByKind(resources)
    
    for _, res := range sorted {
        if err := p.provisionResource(ctx, res); err != nil {
            return fmt.Errorf("failed to provision %s/%s: %w", res.Kind, res.Name, err)
        }
        p.Log.Info("Provisioned pre-upgrade resource", "kind", res.Kind, "name", res.Name)
    }
    
    return nil
}

func (p *EnsurePreUpgradeResources) provisionResource(ctx context.Context, spec ResourceSpec) error {
    switch spec.Kind {
    case "ConfigMap":
        return p.provisionConfigMap(ctx, spec)
    case "Secret":
        return p.provisionSecret(ctx, spec)
    case "CustomResourceDefinition":
        return p.provisionCRD(ctx, spec)
    default:
        return p.provisionFromManifest(ctx, spec)
    }
}

func (p *EnsurePreUpgradeResources) provisionConfigMap(ctx context.Context, spec ResourceSpec) error {
    cm := &corev1.ConfigMap{
        ObjectMeta: metav1.ObjectMeta{
            Name:      spec.Name,
            Namespace: spec.Namespace,
            Labels:    spec.Labels,
        },
        Data: spec.Data,
    }
    
    err := p.client.Create(ctx, cm)
    if apierrors.IsAlreadyExists(err) {
        return nil // 幂等：已存在则跳过
    }
    return err
}

func (p *EnsurePreUpgradeResources) provisionSecret(ctx context.Context, spec ResourceSpec) error {
    secret := &corev1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Name:      spec.Name,
            Namespace: spec.Namespace,
            Labels:    spec.Labels,
        },
        StringData: spec.StringData,
    }
    
    err := p.client.Create(ctx, secret)
    if apierrors.IsAlreadyExists(err) {
        return nil
    }
    return err
}

func (p *EnsurePreUpgradeResources) sortResourcesByKind(resources []ResourceSpec) []ResourceSpec {
    // 按依赖优先级排序：CRD > ConfigMap > Secret > 其他
    kindPriority := map[string]int{
        "CustomResourceDefinition": 0,
        "ConfigMap":                1,
        "Secret":                   2,
    }
    
    sort.Slice(resources, func(i, j int) bool {
        pi := kindPriority[resources[i].Kind]
        pj := kindPriority[resources[j].Kind]
        return pi < pj
    })
    
    return resources
}
```

**注册到 ComponentFactory**：

```go
// cmd/manager/main.go
func main() {
    // ... 初始化控制器
    
    // 注册预创建资源 Phase
    componentFactory.Register(
        "EnsurePreUpgradeResources",
        "v1.0",
        &preupgraderesources.EnsurePreUpgradeResources{
            BasePhase: basePhase,
            Client:    mgr.GetClient(),
        },
    )
}
```

**ReleaseImage 中的引用**：

```yaml
# ReleaseImage OCI 定义
version: "v2.6.0"
ociRef: "registry/openfuyao-release:v2.6.0"
install:
  components:
    - name: kubernetes
      version: v1.29.0
    - name: etcd
      version: v3.5.12
upgrade:
  components:
    - name: pre-upgrade-resources  # 最先执行
      version: v1.0.0
    - name: provider-upgrade
      version: v1.2.0
    - name: etcd-upgrade
      version: v3.5.12
```

**在 DAG 中的执行流程**：

```txt
┌─────────────────────────────────────────────────────────┐
│              升级 DAG 执行顺序                            │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Batch 1:  pre-upgrade-resources                        │
│            ├─ 创建 ConfigMap: bke-new-feature-config    │
│            └─ 创建 Secret: bke-new-cert                 │
│                 │                                       │
│                 ▼                                       │
│  Batch 2:  provider-upgrade                             │
│            └─ 依赖 Batch 1 完成                          │
│                 │                                       │
│                 ▼                                       │
│  Batch 3:  etcd-upgrade                                 │
│            └─ 依赖 Batch 1 完成                          │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

**NeedExecute 触发逻辑**：

```go
func (p *EnsurePreUpgradeResources) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // 1. 基础检查：集群状态是否允许升级
    if !p.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }
    
    // 2. 版本上下文检查：目标版本是否包含此组件
    vCtx := p.GetVersionContext()
    if vCtx == nil {
        return false
    }
    
    currentVer := vCtx.Current["pre-upgrade-resources"]
    targetVer := vCtx.Target["pre-upgrade-resources"]
    
    // 3. 版本不同或首次安装时执行
    if currentVer != targetVer {
        p.Log.Info("Pre-upgrade resources triggered", 
            "current", currentVer, "target", targetVer)
        return true
    }
    
    return false
}
```

**扩展新资源类型**：

如需支持新的资源类型，只需在 `provisionResource` 中添加 case 分支：

```go
func (p *EnsurePreUpgradeResources) provisionResource(ctx context.Context, spec ResourceSpec) error {
    switch spec.Kind {
    case "ConfigMap":
        return p.provisionConfigMap(ctx, spec)
    case "Secret":
        return p.provisionSecret(ctx, spec)
    case "CustomResourceDefinition":
        return p.provisionCRD(ctx, spec)
    case "ServiceAccount":  // 新增类型
        return p.provisionServiceAccount(ctx, spec)
    default:
        return p.provisionFromManifest(ctx, spec)
    }
}
```

## 5. 平滑升级方案（旧版到新版）

### 5.1 自动迁移方案

1. **部署新 CRD 与控制器（FeatureGate 关闭）**：部署 API 与控制器，`DeclarativeUpgrade=false`，保持原有逻辑。
2. **自动创建 ClusterVersion**：`BKEClusterReconciler` 检测到无 CV 关联时，自动创建 CV 实例，`DesiredVersion` 与 `CurrentVersion` 填充为当前 `BKECluster.Spec.OpenFuyaoVersion`。
3. **开启 FeatureGate 切换**：运维开启开关，后续变更由新流程接管。

### 5.2 手工提前构建方案

为支持更可控的灰度与预检，支持手工提前构建 `ReleaseImage` 与 `UpgradePath`：

1. **手工构建 ReleaseImage** oci镜像，并上传到仓库。
   - 内容包含目标版本的所有组件清单。控制器解析后标记 `Status.Phase=Valid`。
2. **手工构建 UpgradePath**
   - 定义从旧版本到新版本的升级路径，支持从旧版本往新版本升级。

## 6. 异常场景、性能、安全与可扩展性设计

### 6.1 异常场景处理

| 异常 | 处理机制 |
| -- | ------ |
| OCI 镜像拉取失败 | 指数退避重试（3次）；本地 ConfigMap fallback；`ReleaseImage` 标记 `Invalid` 并阻断升级 |
| DAG 存在环路 | 拓扑排序前执行 Tarjan 环检测；超时强制中断并记录 `CycleDetected` 事件 |
| Phase 执行失败 | 记录详细 `History`；支持 `AllowDowngrade=true` 自动回滚；提供 `--skip-failed-component` 紧急开关 |
| 上下文注入丢失 | `NeedExecute` 增加 nil 保护；单元测试覆盖上下文生命周期；Feature Gate 灰度 |

### 6.2 性能设计

- **缓存层**：OCI 镜像 Config、`UpgradePath` 规则、`ComponentVersion` 元数据均使用 LRU 缓存（TTL 5m）。
- **异步解析**：OCI 拉取与 DAG 构建采用 goroutine 池，避免阻塞 Reconcile 循环。
- **批量调度**：`Batch` 模式支持并发执行非依赖组件，利用 `errgroup` 控制最大并发数（默认 10）。
- **超时控制**：单 Phase 执行超时默认 30m，可配置；DAG 全局超时 4h。

### 6.3 安全设计

- **镜像签名**：OCI 镜像强制使用 Cosign 签名验证，未通过校验拒绝加载。
- **RBAC 最小权限**：控制器仅授予 `get/list/watch` 自身 CRD 与 `patch` BKECluster 的权限。
- **敏感数据**：证书、kubeconfig 等通过 K8s Secrets 存储，传输全程 mTLS。
- **审计日志**：全链路操作记录至 K8s AuditLog。

### 6.4 可扩展性设计

- **水平扩展**：控制器支持 Leader Election ，支持多实例部署。
- **插件化 Phase**：通过 `ComponentFactory` 注册机制，第三方组件可动态注入 YAML 或 Go 插件。
- **多架构支持**：OCI 镜像支持 `linux/amd64`, `linux/arm64` 多架构清单，自动匹配节点架构。

## 7. 工作量评估

| 阶段 | 任务内容 | 工作量 (人天) | 说明 |
| ----- | ------ | --------- | ------ |
| **1. CRD 与 API** | CV/RI/UP/ComponentVersion 定义、Webhook、DeepCopy | 10 | 新手需学习 kubebuilder 脚手架、CRD 验证规则、不可变字段约束 |
| **2. OCI 解析层** | `go-containerregistry` 集成、Config 解析、缓存机制 | 15 | 镜像分层拉取、鉴权配置、离线 fallback 调试耗时较长 |
| **3. 控制器开发** | CV/RI/UP 控制器逻辑、状态机、Annotation 协同机制 | 18 | controller-runtime 调谐循环、Watch 过滤、Reconcile 幂等性设计 |
| **4. DAG 引擎** | 依赖图构建、拓扑排序、FailurePolicy 分支、并发调度 | 14 | 图算法实现、环检测、并发安全与超时控制易出 Bug |
| **5. Phase 适配** | 4 个 Phase YAML 化、`Version()` 接口、`NeedExecute` 改造、Factory 注册 | 10 | 上下文注入调试、旧逻辑兼容、单元测试覆盖 |
| **6. CSP/SAT 求解** | 约束解析、回溯算法实现、兼容性校验集成 | 8 | 语义化版本库使用、约束冲突路径追踪、边界条件处理 |
| **7. E2E 验证** | 补丁升级、跨版本升级、失败回滚、OCI 降级、预创建资源、多场景集成验证 | 15 | 需搭建真实集群环境、模拟各种异常场景、验证升级全流程 |
| **8. 迁移与测试** | 旧版平滑迁移、OCI 预构建流水线、压测、安全扫描 | 12 | 新手编写集成测试与 Mock 较慢，需反复联调 |
| **总计** | | **102 人天** | 含代码评审、联调缓冲、文档编写 |

### 7.1 独立开发任务拆解

以下按功能模块拆解，确保每个任务 **≤ 40 人天（2人月）**，具备明确接口边界与交付物，可并行开发。经详细拆解后，总工作量评估为 **102 人天**。

| 任务 ID | 功能模块 (管理者视角) | 包含子功能 | 交付物 | 依赖 | 人天 |
| ------- | -------- | ---------- | ------ | ---- | ---- |
| **F1** | 版本管理基础模型 | 定义集群版本、发布镜像、升级路径、组件版本四大核心数据模型，建立版本声明式管理的基础数据结构，支持版本不可变约束与合法性校验 | API 代码、CRD YAML、Webhook 骨架 | 无 | 8 |
| **F2** | 版本清单分发与存储 | 实现从 OCI 镜像仓库拉取版本清单的能力，支持离线环境降级、多级缓存加速、镜像签名安全验证，确保版本数据可靠获取 | `pkg/oci/client.go`、`pkg/manifest/store.go` | F1 | 15 |
| **F3** | 升级路径智能规划 | 构建版本升级路径图，自动计算最短升级路径，检测非法循环路径，实时监控云端路径更新，支持升级前后检查步骤 | `pkg/upgrade/graph.go`、`pkg/upgrade/digest_monitor.go` | F1, F2 | 12 |
| **F4** | 升级任务发起与审批 | 接收用户升级请求，自动校验升级路径合法性与组件兼容性，通过后触发集群升级流程，全程记录审计事件 | `controllers/clusterversion_reconciler.go` | F1, F2, F3 | 10 |
| **F5** | 版本兼容性自动校验 | 自动展开组合组件为叶子组件，校验各组件版本间兼容性约束（如 K8s 与 Etcd 版本匹配），冲突时拦截升级并输出详细报告 | `controllers/releaseimage_reconciler.go`、`pkg/upgrade/compatibility.go` | F1, F2 | 14 |
| **F6** | 升级路径动态管理 | 维护全局升级路径图，实时同步云端最新路径定义，提供路径查询服务供其他模块调用，广播路径变更通知 | `controllers/upgradepath_reconciler.go` | F1, F3 | 6 |
| **F7** | 组件升级插件框架 | 提供组件注册与版本路由机制，支持将升级步骤声明为 YAML 清单，自动注入版本上下文，实现升级前资源预创建 | `pkg/phaseframe/factory.go`、4 个 Phase YAML、`pkg/phase/preupgrade/` | F1 | 12 |
| **F8** | 升级流程编排引擎 | 按组件依赖关系自动编排升级顺序，支持失败策略配置（停止/跳过/回滚），支持无依赖组件并发执行，超时自动中断 | `controllers/bkecluster_dag.go` | F3, F7 | 10 |
| **F9** | 旧系统平滑迁移 | 支持从旧版本无缝切换至新架构，自动为现有集群创建版本声明，提供特性开关控制灰度发布，确保新旧模式双轨运行 | `pkg/featuregate/`、迁移脚本 | F1, F4, F8 | 6 |
| **F10** | 全场景验证与压测 | 覆盖补丁升级、跨版本升级、失败回滚、断网降级等真实场景，验证万级节点并发升级性能，输出压测报告与内存分析 | `test/e2e/` 套件、压测报告 | F1-F9 全部完成 | 9 |

**并行开发建议**：
- **Phase 1 (基础层)**: F1 → F2/F3 可并行启动
- **Phase 2 (控制层)**: F4/F5/F6 在 F1-F3 完成后并行开发
- **Phase 3 (调度层)**: F7/F8 依赖 F1/F3，可与 F4-F6 重叠开发
- **Phase 4 (交付层)**: F9/F10 在核心功能合并后启动

| 原技术术语 | 管理者视角描述 |
| --- | --- |
| CRD 与 API 定义 | 版本管理基础模型 |
| OCI 解析与 ManifestStore | 版本清单分发与存储 |
| UpgradePath 图模型与监控 | 升级路径智能规划 |
| ClusterVersion 控制器 | 升级任务发起与审批 |
| ReleaseImage 控制器与 CSP 求解 | 版本兼容性自动校验 |
| UpgradePath 控制器 | 升级路径动态管理 |
| Phase 框架与组件注册 | 组件升级插件框架 |
| BKECluster DAG 调度引擎 | 升级流程编排引擎 |
| 平滑迁移与 FeatureGate | 旧系统平滑迁移 |
| E2E 测试与压测 | 全场景验证与压测 |
## 8. 架构图与流程图

### 8.1 控制器协同架构图

```txt
用户/CI ──▶ ClusterVersion.Spec.DesiredVersion = "v2.6.0"
               │
               ▼
┌─────────────────────────────────────────────────────────────┐
│ ClusterVersionReconciler                                    │
│  • 拉取 ReleaseImage OCI 解析为 CR                           │
│  • 通过 UpgradePathGraph.FindPath() 查找升级路径             │
│  • 校验 CompatibilityMatrix                                 │
│  • 写入 BKECluster Annotation: upgrade-ready=v2.6.0         │
└──────────────────────────┬──────────────────────────────────┘
                           │ 触发调谐
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ BKEClusterReconciler (增强)                                 │
│  • 捕获 Annotation 变更                                     │
│  • 判断 Install/Upgrade 场景                                │
│  • 构建 VersionContext (Current vs Target Components)       │
│  • 执行 PreUpgradeResourcePhase (预创建 ConfigMap/Secret)   │
│  • 解析 ComponentVersion 依赖 → 构建独立 DAG                 │
│  • 按拓扑顺序调用 Phase，注入 VersionContext                 │
└──────────────────────────┬──────────────────────────────────┘
                           │ 执行升级
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ ComponentFactory / PhaseFrame                               │
│  • Resolve(name, ver) 获取 inline Handler                   │
│  • NeedExecute() 中比对版本                                  │
│  • 版本不同 → 执行原有逻辑                                   │
│  • 版本相同 → 跳过，标记 Succeeded                          │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ UpgradePathReconciler + DigestMonitor                       │
│  • 监控 OCI latest digest 变更 (5m 轮询)                    │
│  • 解析 paths.yaml → 构建 UpgradePathGraph                  │
│  • 环检测 + 路径校验                                        │
│  • 单 CR 聚合所有升级路径                                   │
└─────────────────────────────────────────────────────────────┘
```

## 9. 测试计划与验收标准

| 测试类型 | 覆盖场景 |
| ------ | ----- |
| **单元测试** | DAG 拓扑排序、兼容性矩阵校验、`NeedExecute` 分支逻辑、OCI 解析 |
| **集成测试** | CV ↔ RI ↔ BKECluster 状态联动、Annotation 触发机制、Phase 注册表映射 |
| **E2E 测试** | 补丁升级、跨版本升级、单组件独立升级、失败中断与回滚、OCI 缺失降级 |
| **兼容性测试** | Feature Gate 关闭时旧流程正常运行；新旧版本混合状态平滑过渡 |
| **压测** | 万级节点并发升级、DAG 构建耗时 <2s、内存泄漏检测 |

**毕业标准**：

- **Alpha**: CRD 注册，控制器可启动，日志验证清单解析与路径查找正确。
- **Beta**: 接管升级流程，与旧 Phase 并行运行，结果对比验证，E2E 通过率 >95%。
- **GA**: 全量切换，移除旧版本硬编码调度逻辑，支持生产环境灰度发布。

## 10. 风险与缓解

| 风险 | 影响 | 缓解措施 |
| ------ | ------ | ------- |
| OCI 镜像拉取失败或解析错误 | 升级阻塞 | 指数退避重试；本地 ConfigMap fallback；`ReleaseImage` 提前标记 `Invalid` |
| 依赖图存在环路 | DAG 构建死锁 | 拓扑排序前执行环检测算法；超时强制中断并记录 `CycleDetected` 事件 |
| 兼容性校验误报/漏报 | 升级中断或集群不稳定 | 提供 `--skip-compatibility-check` 紧急开关；规则支持热更新；记录详细审计日志 |
| Phase 上下文注入丢失 | 升级决策错误 | `NeedExecute` 增加 nil 保护；单元测试覆盖上下文生命周期；Feature Gate 灰度 |

## 11. 安装与升级完整 CR 执行样例

### 11.1 场景说明

本样例展示从 **新建集群安装 v2.5.0** 到 **升级到 v2.6.0** 的完整 CR 流转过程。

### 11.2 阶段一：新建集群安装 (v2.5.0)

#### 步骤 1：创建 UpgradePath（全局升级路径定义）

```yaml
# upgradepath.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: UpgradePath
metadata:
  name: openfuyao-upgrade-paths
spec:
  ociRef: "registry/openfuyao-upgradepath:latest"
  paths:
    - from: "v2.4.0"
      to: "v2.5.0"
      blocked: false
      deprecated: true
    - from: "v2.5.0"
      to: "v2.6.0"
      blocked: false
      deprecated: false
    - from: "v2.4.0"
      to: "v2.6.0"
      blocked: true
      notes: "Direct upgrade blocked, please upgrade via v2.5.0"
status:
  phase: Active
  lastDigest: "sha256:abc123..."
  pathCount: 3
```

#### 步骤 2：创建 ReleaseImage v2.5.0（安装清单）

```yaml
# releaseimage-v2.5.0.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: release-v2.5.0
  annotations:
    cvo.openfuyao.cn/oci-digest: "sha256:def456..."
spec:
  version: "v2.5.0"
  ociRef: "registry/openfuyao-release:v2.5.0"
  install:
    components:
      # YAML 类型组件
      - name: kubernetes
        version: v1.28.0
      - name: etcd
        version: v3.5.10
      # Helm 类型组件
      - name: monitoring
        version: v2.0.0
      # Binary 类型组件
      - name: containerd
        version: v1.7.0
      # Inline 类型组件
      - name: pre-upgrade-resources
        version: v1.0.0
        inline:
          handler: EnsurePreUpgradeResources
          version: v1.0
  upgrade:
    components:
      - name: pre-upgrade-resources
        version: v1.0.0
        inline:
          handler: EnsurePreUpgradeResources
          version: v1.0
      - name: provider-upgrade
        version: v1.1.0
      - name: etcd-upgrade
        version: v3.5.10
status:
  phase: Valid
  componentCount: 5
  validatedAt: "2026-05-09T10:00:00Z"
```

#### 步骤 3：创建 BKECluster 与 ClusterVersion（触发安装）

```yaml
# bkecluster.yaml
apiVersion: infra.openfuyao.cn/v1beta1
kind: BKECluster
metadata:
  name: prod-cluster-01
  namespace: default
spec:
  kubernetesVersion: v1.28.0
  etcdVersion: v3.5.10
  networking:
    podCIDR: "10.244.0.0/16"
    serviceCIDR: "10.96.0.0/12"
    dnsDomain: "cluster.local"
  controlPlane:
    replicas: 3
  region: cn-hangzhou
  availabilityZone: cn-hangzhou-a
---
# clusterversion.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ClusterVersion
metadata:
  name: prod-cluster-01-version
  namespace: default
  ownerReferences:
    - apiVersion: infra.openfuyao.cn/v1beta1
      kind: BKECluster
      name: prod-cluster-01
      uid: <bkecluster-uid>
spec:
  desiredVersion: v2.5.0
  releaseImageRef: release-v2.5.0
status:
  currentVersion: ""
  desiredVersion: v2.5.0
  phase: Installing
  installProgress:
    currentComponent: kubernetes
    completedComponents:
      - etcd
    totalComponents: 3
```

**安装执行流程**：

```txt
1. ClusterVersionReconciler 检测到 desiredVersion=v2.5.0，releaseImageRef 已设置
2. 验证 ReleaseImage.Status.Phase=Valid，标记 upgrade-ready Annotation
3. BKEClusterReconciler 检测到 Annotation，判断为 Install 场景
4. 构建 VersionContext (Current 为空，Target 从 ReleaseImage 解析)
5. 构建 TemplateContext (从 BKECluster 提取网络、区域等配置)
6. 解析 ReleaseImage.Spec.Install.Components 构建安装 DAG
7. 按拓扑顺序执行组件 (包含四种类型):
   
   ├─ [Inline] pre-upgrade-resources (最先执行，无依赖)
   │   ├─ GetComponentManifests("pre-upgrade-resources", "v1.0.0", tmplCtx)
   │   ├─ 识别 Type=inline，提取 InlineSpec
   │   ├─ InstallerRegistry 路由到 InlineInstaller
   │   ├─ ComponentFactory.Resolve("EnsurePreUpgradeResources", "v1.0")
   │   ├─ handler.NeedExecute() → true
   │   ├─ handler.Execute():
   │   │   ├─ 创建 ConfigMap: bke-new-feature-config
   │   │   └─ 创建 Secret: bke-new-cert
   │   └─ 标记 Succeeded
   │
   ├─ [Binary] containerd (依赖 pre-upgrade-resources)
   │   ├─ GetComponentManifests("containerd", "v1.7.0", tmplCtx)
   │   ├─ 识别 Type=binary，加载 binary.yaml, install.sh, 压缩包
   │   ├─ InstallerRegistry 路由到 BinaryInstaller
   │   ├─ 校验二进制包 Checksum
   │   ├─ 解压到临时目录
   │   ├─ 渲染 install.sh 脚本 (注入 {{.Version}} 等变量)
   │   └─ 通过 SSH/Agent 在目标节点执行脚本 → 标记 Succeeded
   │
   ├─ [YAML] etcd (依赖 containerd)
   │   ├─ GetComponentManifests("etcd", "v3.5.10", tmplCtx)
   │   ├─ 识别 Type=yaml，扫描 01-crd.yaml, 02-rbac.yaml, 03-statefulset.yaml
   │   ├─ 渲染 YAML 中的模板变量 (如 {{.Namespace}}, {{.ControlPlaneReplicas}})
   │   ├─ InstallerRegistry 路由到 YamlInstaller
   │   ├─ ManifestApplier.ApplyManifests():
   │   │   ├─ Apply 01-crd.yaml
   │   │   ├─ Apply 02-rbac.yaml
   │   │   └─ Apply 03-statefulset.yaml
   │   └─ 标记 Succeeded
   │
   ├─ [YAML] kubernetes (依赖 etcd)
   │   ├─ GetComponentManifests("kubernetes", "v1.28.0", tmplCtx)
   │   ├─ 识别 Type=yaml，扫描 YAML 文件
   │   ├─ 渲染模板变量
   │   ├─ InstallerRegistry 路由到 YamlInstaller
   │   ├─ ApplyManifests(01-crd.yaml, 02-rbac.yaml, 03-configmap.yaml, 04-deployment.yaml)
   │   └─ 标记 Succeeded
   │
   └─ [Helm] monitoring (依赖 kubernetes)
       ├─ GetComponentManifests("monitoring", "v2.0.0", tmplCtx)
       ├─ 识别 Type=helm，加载 Chart.yaml, values.yaml, templates/*
       ├─ 渲染 values.yaml 中的模板变量
       ├─ InstallerRegistry 路由到 HelmInstaller
       ├─ Helm SDK action.NewInstall().Run():
       │   ├─ 合并 pkg.Chart.Values 与 TemplateContext 生成的 Values
       │   ├─ 渲染 templates 模板
       │   └─ 创建 K8s 资源
       └─ 标记 Succeeded

8. 更新 ClusterVersion.Status:
   ├─ currentVersion: v2.5.0
   ├─ phase: Installed
   └─ installProgress: completed
```

### 11.3 阶段二：升级到 v2.6.0

#### 步骤 4：创建 ReleaseImage v2.6.0（目标版本清单）

```yaml
# releaseimage-v2.6.0.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: release-v2.6.0
  annotations:
    cvo.openfuyao.cn/oci-digest: "sha256:ghi789..."
spec:
  version: "v2.6.0"
  ociRef: "registry/openfuyao-release:v2.6.0"
  install:
    components:
      - name: kubernetes
        version: v1.29.0
      - name: etcd
        version: v3.5.12
      - name: bke-provider
        version: v1.2.0
  upgrade:
    components:
      - name: pre-upgrade-resources
        version: v1.0.0
        inline:
          handler: EnsurePreUpgradeResources
          version: v1.0
      - name: provider-upgrade
        version: v1.2.0
        inline:
          handler: EnsureProviderUpgrade
          version: v1.0
      - name: etcd-upgrade
        version: v3.5.12
        inline:
          handler: EnsureEtcdUpgrade
          version: v1.0
status:
  phase: Valid
  componentCount: 6
  validatedAt: "2026-05-10T08:00:00Z"
```

#### 步骤 5：更新 ClusterVersion 触发升级

```yaml
# 更新 clusterversion.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ClusterVersion
metadata:
  name: prod-cluster-01-version
  namespace: default
spec:
  desiredVersion: v2.6.0          # 从 v2.5.0 改为 v2.6.0
  releaseImageRef: release-v2.6.0 # 指向新版本
status:
  currentVersion: v2.5.0          # 保持当前版本
  desiredVersion: v2.6.0
  phase: Upgrading
  upgradeProgress:
    currentComponent: pre-upgrade-resources
    completedComponents: []
    totalComponents: 3
    path:
      - from: v2.5.0
        to: v2.6.0
```

**升级执行流程**：

```txt
1. ClusterVersionReconciler 检测到 desiredVersion 从 v2.5.0 变更为 v2.6.0
2. 调用 UpgradePathGraph.FindPath("v2.5.0", "v2.6.0") 获取路径
   ├─ 返回: [{from: v2.5.0, to: v2.6.0, blocked: false}]
   └─ 路径合法，继续
3. 验证 ReleaseImage v2.6.0.Status.Phase=Valid
4. 执行兼容性校验 (CSP 求解器)
   ├─ 检查 kubernetes v1.29.0 与 etcd v3.5.12 兼容性
   └─ 通过
5. 标记 BKECluster Annotation: cvo.openfuyao.cn/upgrade-ready=v2.6.0
6. BKEClusterReconciler 检测到 Annotation，判断为 Upgrade 场景
7. 构建 VersionContext:
   ├─ Current: {kubernetes: v1.28.0, etcd: v3.5.10, bke-provider: v1.0.0}
   └─ Target:  {kubernetes: v1.29.0, etcd: v3.5.12, bke-provider: v1.2.0}
8. 构建 TemplateContext (复用 BKECluster 配置)
9. 解析 ReleaseImage.Spec.Upgrade.Components 构建升级 DAG
10. 按拓扑顺序执行组件:
    ├─ pre-upgrade-resources (inline 组件，最先执行)
    │   ├─ GetComponentManifests("pre-upgrade-resources", "v1.0.0", tmplCtx)
    │   ├─ Inline 分支: compRef.Inline != nil
    │   ├─ ComponentFactory.Resolve("EnsurePreUpgradeResources", "v1.0", inline)
    │   ├─ NeedExecute() → true (targetVer != currentVer)
    │   ├─ Execute():
    │   │   ├─ 创建 ConfigMap: bke-new-feature-config
    │   │   └─ 创建 Secret: bke-new-cert
    │   └─ 标记 Succeeded
    ├─ provider-upgrade (inline 组件)
    │   ├─ GetComponentManifests("provider-upgrade", "v1.2.0", tmplCtx)
    │   ├─ Inline 分支: compRef.Inline != nil
    │   ├─ ComponentFactory.Resolve("EnsureProviderUpgrade", "v1.0", inline)
    │   ├─ NeedExecute() → true (v1.0.0 → v1.2.0)
    │   ├─ Execute():
    │   │   ├─ 滚动更新 bke-provider Deployment
    │   │   └─ 验证 Provider 健康状态
    │   └─ 标记 Succeeded
    └─ etcd-upgrade (inline 组件)
        ├─ GetComponentManifests("etcd-upgrade", "v3.5.12", tmplCtx)
        ├─ Inline 分支: compRef.Inline != nil
        ├─ ComponentFactory.Resolve("EnsureEtcdUpgrade", "v1.0", inline)
        ├─ NeedExecute() → true (v3.5.10 → v3.5.12)
        ├─ Execute():
        │   ├─ 执行 etcd 备份
        │   ├─ 逐个滚动升级 etcd Pod
        │   └─ 验证 etcd 集群健康
        └─ 标记 Succeeded
11. 更新 ClusterVersion.Status:
    ├─ currentVersion: v2.6.0
    ├─ currentReleaseImageRef: release-v2.6.0
    ├─ phase: Upgraded
    └─ upgradeProgress: completed
12. 清除 BKECluster Annotation: cvo.openfuyao.cn/upgrade-ready
```

### 11.3.1 阶段三：多跳升级场景 (v2.4.0 → v2.6.0)

#### 场景说明

集群当前版本为 **v2.4.0**，目标升级到 **v2.6.0**。UpgradePath 图计算出的最短路径为：

```
v2.4.0 → v2.5.0 → v2.6.0
```

共 **2 跳**，利用 Kubernetes 控制器自然调谐循环逐跳执行。

#### 步骤 1：UpgradePath 定义（包含多跳路径）

```yaml
# upgradepath.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: UpgradePath
metadata:
  name: openfuyao-upgrade-paths
spec:
  ociRef: "registry/openfuyao-upgradepath:latest"
  paths:
    - from: "v2.4.0"
      to: "v2.5.0"
      blocked: false
      deprecated: false
    - from: "v2.5.0"
      to: "v2.6.0"
      blocked: false
      deprecated: false
    - from: "v2.4.0"
      to: "v2.6.0"
      blocked: true
      notes: "Direct upgrade blocked, requires v2.5.0 as stepping stone"
status:
  phase: Active
  lastDigest: "sha256:multi123..."
  pathCount: 3
```

#### 步骤 2：创建 ReleaseImage v2.5.0 和 v2.6.0

```yaml
# releaseimage-v2.5.0.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: release-v2.5.0
  annotations:
    cvo.openfuyao.cn/oci-digest: "sha256:abc123..."
spec:
  version: "v2.5.0"
  ociRef: "registry/openfuyao-release:v2.5.0"
  install:
    components:
      - name: kubernetes
        version: v1.28.0
      - name: etcd
        version: v3.5.10
  upgrade:
    components:
      - name: provider-upgrade
        version: v1.1.0
        inline:
          handler: EnsureProviderUpgrade
          version: v1.0
      - name: etcd-upgrade
        version: v3.5.10
        inline:
          handler: EnsureEtcdUpgrade
          version: v1.0
status:
  phase: Valid
---
# releaseimage-v2.6.0.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ReleaseImage
metadata:
  name: release-v2.6.0
  annotations:
    cvo.openfuyao.cn/oci-digest: "sha256:def456..."
spec:
  version: "v2.6.0"
  ociRef: "registry/openfuyao-release:v2.6.0"
  install:
    components:
      - name: kubernetes
        version: v1.29.0
      - name: etcd
        version: v3.5.12
  upgrade:
    components:
      - name: provider-upgrade
        version: v1.2.0
        inline:
          handler: EnsureProviderUpgrade
          version: v1.0
      - name: etcd-upgrade
        version: v3.5.12
        inline:
          handler: EnsureEtcdUpgrade
          version: v1.0
status:
  phase: Valid
```

#### 步骤 3：更新 ClusterVersion 触发多跳升级

```yaml
# clusterversion.yaml (初始状态)
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ClusterVersion
metadata:
  name: prod-cluster-01-version
  namespace: default
spec:
  desiredVersion: v2.6.0          # 目标版本
  releaseImageRef: release-v2.6.0
status:
  currentVersion: v2.4.0          # 当前版本
  desiredVersion: v2.6.0
  phase: Upgrading
  upgradeHistory: []
```

**多跳升级执行流程（基于自然调谐循环）**：

```txt
【第一次 Reconcile：执行第 1 跳 v2.4.0 → v2.5.0】

1. ClusterVersionReconciler 检测到:
   ├─ CurrentVersion: v2.4.0
   └─ DesiredVersion: v2.6.0
   └─ CurrentVersion != DesiredVersion → 继续
2. 计算升级路径:
   └─ FindPath("v2.4.0", "v2.6.0") → [v2.4.0→v2.5.0, v2.5.0→v2.6.0]
3. 获取第一跳目标: hopTarget = v2.5.0
4. 检查 BKECluster 状态:
   ├─ 若 Version == v2.5.0 → 第 1 跳已完成，更新 CurrentVersion，触发下一次 Reconcile
   └─ 若 Version != v2.5.0 → 继续
5. 触发第 1 跳升级:
   ├─ 写入 Annotation: upgrade-ready=v2.5.0
   └─ 写入 Annotation: upgrade-hop=v2.4.0→v2.5.0
6. BKEClusterReconciler 执行 v2.5.0 升级 DAG:
   ├─ provider-upgrade (v1.0.0 → v1.1.0)
   └─ etcd-upgrade (v3.5.9 → v3.5.10)
7. BKECluster 升级完成，更新 Status.Version=v2.5.0

【第二次 Reconcile：执行第 2 跳 v2.5.0 → v2.6.0】
(CurrentVersion 变更触发新的 Reconcile)

8. ClusterVersionReconciler 检测到:
   ├─ CurrentVersion: v2.5.0 (已更新)
   └─ DesiredVersion: v2.6.0
   └─ CurrentVersion != DesiredVersion → 继续
9. 重新计算升级路径:
   └─ FindPath("v2.5.0", "v2.6.0") → [v2.5.0→v2.6.0]
10. 获取第一跳目标: hopTarget = v2.6.0
11. 检查 BKECluster 状态:
    ├─ 若 Version == v2.6.0 → 第 2 跳已完成，更新 CurrentVersion，触发下一次 Reconcile
    └─ 若 Version != v2.6.0 → 继续
12. 触发第 2 跳升级:
    ├─ 写入 Annotation: upgrade-ready=v2.6.0
    └─ 写入 Annotation: upgrade-hop=v2.5.0→v2.6.0
13. BKEClusterReconciler 执行 v2.6.0 升级 DAG:
    ├─ provider-upgrade (v1.1.0 → v1.2.0)
    └─ etcd-upgrade (v3.5.10 → v3.5.12)
14. BKECluster 升级完成，更新 Status.Version=v2.6.0

【第三次 Reconcile：升级完成】
(CurrentVersion 变更触发新的 Reconcile)

15. ClusterVersionReconciler 检测到:
    ├─ CurrentVersion: v2.6.0 (已更新)
    └─ DesiredVersion: v2.6.0
    └─ CurrentVersion == DesiredVersion → 升级完成
16. 更新 ClusterVersion.Status:
    ├─ phase: Upgraded
    └─ upgradeHistory: [{v2.4.0→v2.5.0}, {v2.5.0→v2.6.0}]
17. 清除 BKECluster Annotation: upgrade-ready, upgrade-hop
```

**多跳升级状态流转表**：

| Reconcile 次数 | CurrentVersion | DesiredVersion | 计算路径 | 执行跳 | BKECluster Annotation | 说明 |
| -------------- | -------------- | -------------- | -------- | ------ | --------------------- | ---- |
| 第 1 次 | v2.4.0 | v2.6.0 | v2.4.0→v2.5.0→v2.6.0 | v2.4.0→v2.5.0 | upgrade-ready=v2.5.0 | 执行第 1 跳 |
| 第 2 次 | v2.5.0 | v2.6.0 | v2.5.0→v2.6.0 | v2.5.0→v2.6.0 | upgrade-ready=v2.6.0 | 执行第 2 跳 |
| 第 3 次 | v2.6.0 | v2.6.0 | - | - | 无 | 升级完成 |

### 11.4 CR 关联关系图

```txt
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                               │
│  name: prod-cluster-01                                          │
│  spec.kubernetesVersion: v1.29.0 (升级后)                       │
│  spec.etcdVersion: v3.5.12 (升级后)                             │
└──────────────────────────┬──────────────────────────────────────┘
                           │ OwnerReference (1:1)
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                      ClusterVersion                              │
│  name: prod-cluster-01-version                                   │
│  spec.desiredVersion: v2.6.0                                     │
│  spec.releaseImageRef: release-v2.6.0 ──────────────────────┐   │
│  status.currentVersion: v2.6.0                              │   │
│  status.phase: Upgraded                                     │   │
└──────────────────────────────────────────────────────────────┼───┘
                                                               │
                                                               │ 引用
                                                               ▼
┌──────────────────────────────────────────────────────────────────┐
│                       ReleaseImage                               │
│  name: release-v2.6.0                                           │
│  spec.version: v2.6.0                                           │
│  spec.install.components: [{kubernetes: v1.29.0}, {etcd: v3.5.12}]│
│  spec.upgrade.components: [{pre-upgrade-resources}, {provider},  │
│                            {etcd-upgrade}]                       │
└──────────────────────────────────────────────────────────────────┘
       │                                                           
       │ 按 (name, version) 定位                                   
       ▼                                                           
┌──────────────────────────────────────────────────────────────────┐
│              bke-manifests (ComponentVersion)                    │
│  kubernetes/v1.29.0/component.yaml                               │
│  etcd/v3.5.12/component.yaml                                     │
│  pre-upgrade-resources/v1.0.0/component.yaml                     │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│                       UpgradePath                                │
│  name: openfuyao-upgrade-paths                                   │
│  spec.paths: [{v2.5.0→v2.6.0, blocked: false}]                   │
│  status.phase: Active                                            │
└──────────────────────────────────────────────────────────────────┘
```

### 11.5 关键状态流转

| 阶段 | ClusterVersion.Status.Phase | BKECluster.Annotation | 说明 |
| ---- | --------------------------- | --------------------- | ---- |
| 初始 | `Pending` | 无 | 新集群，未关联版本 |
| 安装中 | `Installing` | 无 | 正在执行安装 DAG |
| 安装完成 | `Installed` | 无 | 所有组件安装成功 |
| 就绪 | `Ready` | 无 | `CurrentVersion` == `DesiredVersion`，等待升级 |
| 预检中 | `PreChecking` | 无 | 校验升级路径与兼容性 |
| 升级请求 | `PreChecking` | 无 | 用户修改 desiredVersion，开始预检 |
| 预检拦截 | `Blocked` | 无 | 升级路径不存在或被拦截 |
| 预检失败 | `PreCheckFailed` | 无 | 组件兼容性校验失败 |
| 升级中 | `Upgrading` | `upgrade-ready=v2.6.0` | 预检通过，正在执行升级 DAG |
| 升级完成 | `Upgraded` | 无 (已清除) | 升级 DAG 执行成功，即将转为 Ready |

### 11.6 各类型组件安装样例

本节详细展示四种组件类型（YAML、Helm、Binary、Inline）在 ManifestStore 中的目录结构、ComponentVersion 定义以及执行流程。

#### 11.6.1 YAML 组件类型样例

**场景**：安装 `etcd` 组件，使用标准 Kubernetes YAML 清单。

**1. 目录结构**：

```txt
bke-manifests/
└── etcd/
    └── v3.5.12/
        ├── component.yaml          # 组件元数据
        ├── 01-crd.yaml             # CRD 定义
        ├── 02-rbac.yaml            # RBAC 资源
        └── 03-statefulset.yaml     # StatefulSet 工作负载
```

**2. ComponentVersion 定义**：

```yaml
# component.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: etcd-v3.5.12
spec:
  name: etcd
  type: yaml                        # 声明为 yaml 类型
  version: v3.5.12
  upgradeStrategy:
    mode: Batch
    batchSize: 1
    timeout: "30m"
    failurePolicy: Rollback
```

**3. 执行流程**：

```txt
1. ManifestStore.GetComponentManifests("etcd", "v3.5.12", tmplCtx)
   ├─ 读取 component.yaml，识别 Type=yaml
   ├─ 扫描目录下 *.yaml 文件 (01-crd.yaml, 02-rbac.yaml, 03-statefulset.yaml)
   ├─ 按文件名排序
   └─ 若传入 tmplCtx，渲染 YAML 中的模板变量 (如 {{.Namespace}})
2. InstallerRegistry.InstallComponent(ctx, pkg, vCtx)
   ├─ 路由到 YamlInstaller
   └─ 调用 ManifestApplier.ApplyManifests()
       ├─ Apply 01-crd.yaml
       ├─ Apply 02-rbac.yaml
       └─ Apply 03-statefulset.yaml
```

#### 11.6.2 Helm 组件类型样例

**场景**：安装 `monitoring` 组件，使用 Helm Chart 包。

**1. 目录结构**：

```txt
bke-manifests/
└── monitoring/
    └── v2.0.0/
        ├── component.yaml          # 组件元数据
        ├── Chart.yaml              # Helm Chart 元数据
        ├── values.yaml             # 默认 values
        └── templates/
            ├── deployment.yaml
            └── service.yaml
```

**2. ComponentVersion 定义**：

```yaml
# component.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: monitoring-v2.0.0
spec:
  name: monitoring
  type: helm                        # 声明为 helm 类型
  version: v2.0.0
  upgradeStrategy:
    mode: Batch
    batchSize: 1
    timeout: "15m"
    failurePolicy: Continue
```

**3. 执行流程**：

```txt
1. ManifestStore.GetComponentManifests("monitoring", "v2.0.0", tmplCtx)
   ├─ 读取 component.yaml，识别 Type=helm
   ├─ 加载 Chart.yaml, values.yaml, templates/* 到 pkg.Chart
   └─ 渲染 values.yaml 中的模板变量
2. InstallerRegistry.InstallComponent(ctx, pkg, vCtx)
   ├─ 路由到 HelmInstaller
   └─ 调用 Helm SDK:
       ├─ 检查 Release 是否存在
       ├─ 若不存在 → action.NewInstall().Run()
       └─ 若存在 → action.NewUpgrade().Run()
           └─ 合并 pkg.Chart.Values 与 TemplateContext 生成的 Values
```

#### 11.6.3 Binary 组件类型样例

**场景**：安装 `containerd` 组件，使用二进制安装包。

**1. 目录结构**：

```txt
bke-manifests/
└── containerd/
    └── v1.7.0/
        ├── component.yaml          # 组件元数据
        ├── binary.yaml             # 二进制包元数据
        ├── containerd-1.7.0.tar.gz # 二进制压缩包
        └── install.sh              # 安装脚本
```

**2. ComponentVersion 定义**：

```yaml
# component.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: containerd-v1.7.0
spec:
  name: containerd
  type: binary                      # 声明为 binary 类型
  version: v1.7.0
  upgradeStrategy:
    mode: Batch
    batchSize: 1
    timeout: "20m"
    failurePolicy: FailFast
```

**3. 执行流程**：

```txt
1. ManifestStore.GetComponentManifests("containerd", "v1.7.0", tmplCtx)
   ├─ 读取 component.yaml，识别 Type=binary
   ├─ 解析 binary.yaml 获取 DownloadURL, Checksum
   ├─ 加载 install.sh 到 pkg.Binary.InstallScript
   └─ 缓存 containerd-1.7.0.tar.gz 到本地
2. InstallerRegistry.InstallComponent(ctx, pkg, vCtx)
   ├─ 路由到 BinaryInstaller
   └─ 执行安装逻辑:
       ├─ 校验二进制包 Checksum
       ├─ 解压到临时目录
       ├─ 渲染 install.sh 脚本 (注入 {{.Version}} 等变量)
       └─ 通过 SSH/Agent 在目标节点执行脚本
```

#### 11.6.4 Inline 组件类型样例

**场景**：执行 `pre-upgrade-resources` 预升级资源创建，使用内嵌 Go 代码。

**1. 目录结构**：

```txt
bke-manifests/
└── pre-upgrade-resources/
    └── v1.0.0/
        └── component.yaml          # 仅包含元数据，无其他文件
```

**2. ComponentVersion 定义**：

```yaml
# component.yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: pre-upgrade-resources-v1.0.0
spec:
  name: pre-upgrade-resources
  type: inline                      # 声明为 inline 类型
  version: v1.0.0
  inline:
    handler: EnsurePreUpgradeResources  # 已注册的 Phase 处理器名称
    version: v1.0
  dependencies: []
  upgradeStrategy:
    mode: Batch
    batchSize: 1
    timeout: "10m"
    failurePolicy: FailFast
```

**3. 执行流程**：

```txt
1. ManifestStore.GetComponentManifests("pre-upgrade-resources", "v1.0.0", tmplCtx)
   ├─ 读取 component.yaml，识别 Type=inline
   ├─ 提取 pkg.Inline = {Handler: "EnsurePreUpgradeResources", Version: "v1.0"}
   └─ 无需加载额外文件
2. InstallerRegistry.InstallComponent(ctx, pkg, vCtx)
   ├─ 路由到 InlineInstaller
   └─ 执行 Phase 逻辑:
       ├─ ComponentFactory.Resolve("EnsurePreUpgradeResources", "v1.0")
       ├─ 注入 VersionContext 和 ComponentPackage
       ├─ 调用 handler.NeedExecute() 判断是否需要执行
       └─ 调用 handler.Execute(ctx) 执行预创建逻辑
```

## 12. 附录：CRD 定义

本节提供 `ClusterVersion`、`ReleaseImage`、`UpgradePath` 和 `ComponentVersion` 的完整 Kubernetes CRD (CustomResourceDefinition) YAML 定义。

### 12.1 ClusterVersion CRD

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: clusterversions.cvo.openfuyao.cn
spec:
  group: cvo.openfuyao.cn
  names:
    kind: ClusterVersion
    listKind: ClusterVersionList
    plural: clusterversions
    singular: clusterversion
    shortNames:
      - cv
  scope: Namespaced
  versions:
    - name: v1beta1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                desiredVersion:
                  type: string
                  description: "Target version for the cluster"
                releaseImageRef:
                  type: string
                  description: "Reference to the ReleaseImage resource"
            status:
              type: object
              properties:
                currentVersion:
                  type: string
                  description: "Current version of the cluster"
                phase:
                  type: string
                  enum: ["Pending", "Installing", "Installed", "Ready", "PreChecking", "Upgrading", "Upgraded", "Blocked", "PreCheckFailed", "Failed"]
                upgradeHistory:
                  type: array
                  items:
                    type: object
                    properties:
                      from: { type: string }
                      to: { type: string }
                      startedAt: { type: string, format: date-time }
                      completedAt: { type: string, format: date-time }
                      status: { type: string, enum: ["Succeeded", "Failed", "RolledBack"] }
                conditions:
                  type: array
                  items:
                    type: object
                    properties:
                      type: { type: string }
                      status: { type: string, enum: ["True", "False", "Unknown"] }
                      reason: { type: string }
                      message: { type: string }
                      lastTransitionTime: { type: string, format: date-time }
```

### 12.2 ReleaseImage CRD

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: releaseimages.cvo.openfuyao.cn
spec:
  group: cvo.openfuyao.cn
  names:
    kind: ReleaseImage
    listKind: ReleaseImageList
    plural: releaseimages
    singular: releaseimage
    shortNames:
      - ri
  scope: Namespaced
  versions:
    - name: v1beta1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                version: { type: string }
                ociRef: { type: string }
                install:
                  type: object
                  properties:
                    components:
                      type: array
                      items:
                        type: object
                        properties:
                          name: { type: string }
                          version: { type: string }
                upgrade:
                  type: object
                  properties:
                    components:
                      type: array
                      items:
                        type: object
                        properties:
                          name: { type: string }
                          version: { type: string }
                          inline:
                            type: object
                            properties:
                              handler: { type: string }
                              version: { type: string }
            status:
              type: object
              properties:
                phase:
                  type: string
                  enum: ["Valid", "Invalid", "ManifestMissing", "CompatibilityFailed"]
                componentCount: { type: integer }
                validatedAt: { type: string, format: date-time }
```

### 12.3 UpgradePath CRD

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: upgradepaths.cvo.openfuyao.cn
spec:
  group: cvo.openfuyao.cn
  names:
    kind: UpgradePath
    listKind: UpgradePathList
    plural: upgradepaths
    singular: upgradepath
    shortNames:
      - up
  scope: Cluster
  versions:
    - name: v1beta1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                ociRef: { type: string }
                paths:
                  type: array
                  items:
                    type: object
                    properties:
                      from: { type: string }
                      to: { type: string }
                      blocked: { type: boolean }
                      deprecated: { type: boolean }
                      notes: { type: string }
                      preCheck:
                        type: array
                        items:
                          type: object
                          properties:
                            name: { type: string }
                            required: { type: boolean }
            status:
              type: object
              properties:
                phase: { type: string, enum: ["Active", "Invalid"] }
                lastDigest: { type: string }
                pathCount: { type: integer }
                lastCheckedAt: { type: string, format: date-time }
```

### 12.4 ComponentVersion CRD

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: componentversions.cvo.openfuyao.cn
spec:
  group: cvo.openfuyao.cn
  names:
    kind: ComponentVersion
    listKind: ComponentVersionList
    plural: componentversions
    singular: componentversion
    shortNames:
      - cv
  scope: Namespaced
  versions:
    - name: v1beta1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                name: { type: string }
                type: { type: string, enum: ["yaml", "helm", "inline", "binary"] }
                version: { type: string }
                inline:
                  type: object
                  properties:
                    handler: { type: string }
                    version: { type: string }
                subComponents:
                  type: array
                  items:
                    type: object
                    properties:
                      name: { type: string }
                      version: { type: string }
                compatibility:
                  type: object
                  properties:
                    constraints:
                      type: array
                      items:
                        type: object
                        properties:
                          component: { type: string }
                          rule: { type: string }
                dependencies:
                  type: array
                  items:
                    type: object
                    properties:
                      name: { type: string }
                      phase: { type: string }
                upgradeStrategy:
                  type: object
                  properties:
                    mode: { type: string }
                    batchSize: { type: integer }
                    timeout: { type: string }
                    failurePolicy: { type: string, enum: ["FailFast", "Continue", "Rollback"] }
                resources:
                  type: array
                  items:
                    type: object
                    x-kubernetes-preserve-unknown-fields: true
```

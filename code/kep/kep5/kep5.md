# KEP-2: 基于 ClusterVersion/ReleaseImage/UpgradePath 的声明式集群版本升级

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-2 |
| **标题** | 声明式集群版本管理：基于 ClusterVersion/ReleaseImage/UpgradePath 的 DAG 驱动升级方案 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-04-22 |
| **依赖** | 现有 PhaseFrame 架构、bke-manifests 镜像构建流程、CAPI v1beta1 |

## 1. 摘要
本提案引入 `ClusterVersion`、`ReleaseImage` 与 `UpgradePath` 三个核心 CRD，借鉴 OpenShift CVO 声明式版本管理理念，结合 OCI 镜像分发版本清单，实现 openFuyao 集群的版本升级。方案保持 `BKEClusterReconciler` 为主调度器，通过解析 `ReleaseImage` 构建独立的 **安装 DAG** 与 **升级 DAG**，按拓扑顺序调用 Phase。Phase 升级决策完全复用现有 `NeedExecute()` 接口，通过注入版本上下文比对当前与目标版本。`bke-manifests` 提供 `ComponentVersion` 清单，支持叶子/组合组件、兼容性约束、依赖拓扑、升级策略及 `inline` 代码引用。架构彻底解耦，支持组件独立演进、平滑迁移与商业化生产级高可用。

## 2. 动机
### 2.1 现状痛点
| 问题 | 现状 | 影响 |
|------|------|------|
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
7. 满足商业化生产级要求（高可用、安全、性能、可观测）。

### 2.3 非目标
1. 不替换 CAPI 核心控制器（KCP/MD）的节点生命周期管理。
2. 不修改 `bke-manifests` 现有构建与分发流程。
3. 不在此阶段实现 UI/CLI 交互层或多集群版本同步。
4. 不重写现有 Phase 核心执行逻辑，仅增强触发决策层与上下文注入。

## 3. 范围与约束
### 3.1 范围
| 范围 | 说明 |
|------|------|
| CRD 定义与注册 | `ClusterVersion`、`ReleaseImage`、`UpgradePath` API 定义 |
| `bke-manifests` 扩展 | 新增 `ComponentVersion` 元数据规范与目录结构 |
| 控制器实现 | 版本声明器、清单验证器、DAG 调度器 |
| 升级路径与兼容性 | 规则引擎、拦截机制、约束求解算法 |
| Phase 适配 | `NeedExecute` 上下文注入、`inline` 映射、`Version()` 接口、ComponentFactory 注册 |
| 生产级保障 | 异常恢复、性能优化、安全加固、水平扩展 |

### 3.2 约束
| 约束 | 说明 |
|------|------|
| **1:1 映射** | 单集群活跃状态下，1 个 `ClusterVersion` 严格对应 1 个 `ReleaseImage` |
| **清单不可变** | `ReleaseImage.Spec` 创建后不可修改 |
| **向后兼容** | 必须支持从现有 `BKECluster` 平滑迁移，Feature Gate 控制开关 |
| **离线环境** | 所有资源通过 OCI/本地缓存提供，支持断网降级 |
| **接口复用** | **严禁新增 `ShouldUpgrade()` 接口**，必须复用 `NeedExecute()` |
| **商业化标准** | 满足 99.95% SLA，支持万级节点集群，全链路审计与加密 |

## 4. 提案设计

### 4.1 资源属性与关联关系
```
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                               │
│  (集群实例，生命周期管理)                                        │
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
type ComponentVersionSpec struct {
    Name            string              `json:"name"`
    Type            ComponentType       `json:"type"`
    Version         string              `json:"version"`
    Inline          *InlineSpec         `json:"inline,omitempty"`
    SubComponents   []SubComponent      `json:"subComponents,omitempty"`
    Compatibility   CompatibilitySpec   `json:"compatibility,omitempty"`
    Dependencies    []Dependency        `json:"dependencies,omitempty"`
    UpgradeStrategy UpgradeStrategySpec `json:"upgradeStrategy,omitempty"`
}

type InlineSpec struct {
    Handler string `json:"handler"`
    Version string `json:"version"`
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
|-----------|---------|-------------------|----------|
| `EnsureProviderSelfUpgrade` | 转为 YAML 清单 | `provider-upgrade/v1.0.0/component.yaml` | 无 |
| `EnsureAgentUpgrade` | 转为 YAML 清单 | `bkeagent-upgrade/v1.0.0/component.yaml` | 无 |
| `EnsureComponentUpgrade` | 转为 YAML 清单 | `component-upgrade/v1.0.0/component.yaml` | 无 |
| `EnsureEtcdUpgrade` | 转为 YAML 清单 | `etcd-upgrade/v1.0.0/component.yaml` | 无 |
| 其他代码 Phase | 增加 `Version()` 接口，注册至 Factory | 不生成 YAML | `Version() string`, `NeedExecute()` 增强 |

### 4.4 bke-manifests 目录与 OCI 镜像设计
#### 4.4.1 bke-manifests 目录规范
取消 `phases` 层，统一放在第一层：
```
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
    - name: provider-upgrade
      version: v1.2.0
```

#### 4.4.3 UpgradePath OCI 样例 (YAML)
```yaml
paths:
  - from: "v2.5.0"
    to: "v2.6.0"
    blocked: false
```
*(注：preCheck/postCheck 暂不实现)*

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
```
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
│    构建约束矩阵             │
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
│ 4. 结果判定                 │
│    ├─ 无冲突 → Valid        │
│    └─ 有冲突 → Invalid      │
│       返回冲突路径与规则    │
└─────────────────────────────┘
```

#### 4.5.1 SAT/CSP 兼容性求解设计
**选型**：采用 `github.com/Masterminds/semver/v3` 进行语义化版本解析与约束匹配，结合轻量级 **CSP（约束满足问题）回溯算法**。K8s 生态中版本依赖本质是 CSP 而非布尔 SAT，回溯求解更贴合实际。
**使用方法**：
1. **变量定义**：每个组件为一个变量，定义域为其可用版本列表。
2. **约束转换**：将 `rule: ">=3.5.10"` 转换为 `semver.Constraints` 对象。
3. **拓扑排序**：按 `dependencies` 构建有向无环图，确定变量赋值顺序。
4. **约束传播与回溯**：
   - 按拓扑序依次为组件分配版本。
   - 每次分配后，检查该组件的 `constraints` 是否与已分配的其他组件版本冲突。
   - 若冲突，回溯至上一个组件尝试其他版本；若所有版本均冲突，返回 `Invalid` 并输出冲突路径。
```go
func SolveCompatibility(components []ComponentRef, store *ManifestStore) error {
    vars := buildVariables(components, store)
    constraints := buildConstraints(components, store)
    order := topologicalSort(components)
    
    return backtrack(0, order, vars, constraints)
}

func backtrack(idx int, order []string, vars map[string]string, constraints map[string][]*semver.Constraints) error {
    if idx == len(order) { return nil }
    comp := order[idx]
    for _, ver := range vars[comp].AvailableVersions {
        if satisfiesConstraints(comp, ver, constraints, vars) {
            vars[comp].Assigned = ver
            if err := backtrack(idx+1, order, vars, constraints); err == nil {
                return nil
            }
        }
    }
    return fmt.Errorf("no valid version for component %s", comp)
}
```

#### 4.5.2 manifestStore 实现
```go
// pkg/manifest/store.go
type ManifestStore struct {
    cache   *sync.Map // key: "{name}@{version}" -> *ComponentVersion
    ociClient *oci.Client
}

func (s *ManifestStore) Get(name, version string) (*ComponentVersion, error) {
    key := fmt.Sprintf("%s@%s", name, version)
    if val, ok := s.cache.Load(key); ok {
        return val.(*ComponentVersion), nil
    }
    
    // 1. 尝试从本地 bke-manifests 目录加载
    localPath := fmt.Sprintf("/etc/bke/manifests/%s/%s/component.yaml", name, version)
    if data, err := os.ReadFile(localPath); err == nil {
        cv := &ComponentVersion{}
        if yaml.Unmarshal(data, cv) == nil {
            s.cache.Store(key, cv)
            return cv, nil
        }
    }
    
    // 2. 降级：从 OCI 拉取对应层的 component.yaml
    img, err := s.ociClient.Pull(fmt.Sprintf("registry/openfuyao-release:%s", version))
    if err != nil { return nil, err }
    
    layer, err := img.GetLayerByPath(fmt.Sprintf("%s/%s/component.yaml", name, version))
    if err != nil { return nil, err }
    
    cv := &ComponentVersion{}
    if err := yaml.Unmarshal(layer.Content, cv); err != nil { return nil, err }
    
    s.cache.Store(key, cv)
    return cv, nil
}

func (s *ManifestStore) GetReleaseImage(version string) (*ReleaseImage, error) {
    // 类似逻辑，解析 OCI config.json 或 release.yaml
    // ...
}
```
### 4.6 控制器架构与逻辑
| 控制器 | 核心职责 | 协同方式 |
|--------|---------|----------|
| **ClusterVersionReconciler** | 版本声明管理、ReleaseImage/UpgradePath OCI 拉取与解析、触发 BKECluster 调谐 | 更新 BKECluster Annotation `cvo.openfuyao.cn/upgrade-ready` |
| **ReleaseImageReconciler** | 清单校验、兼容性矩阵验证、bke-manifests 路径验证、状态标记 | 独立调谐，更新 `Status.Phase` |
| **UpgradePathReconciler** | 路径规则验证、拦截/废弃状态维护、使用统计 | 提供 `FindUpgradePath()` 接口供 CV 调用 |
| **BKEClusterReconciler** (增强) | 监听版本变更、构建 VersionContext、DAG 调度、Phase 桥接、状态回写 | 直接 Watch CV；从 ComponentFactory 获取 Phase；更新 CV Status |

**核心代码片段 (DAG 调度与 Factory 调用)**：
```go
func (r *BKEClusterReconciler) executeDAG(ctx context.Context, bc *bkev1beta1.BKECluster, dag *topology.DAG, scenario string) error {
    vCtx := r.buildVersionContext(bc)
    for _, batch := range dag.TopologicalSort() {
        var errs []error
        for _, compName := range batch {
            // 从 ComponentFactory 获取 Phase 实例
            compRef := dag.GetComponent(compName)
            inst, err := r.componentFactory.Resolve(compName, compRef.Version, compRef.Inline)
            if err != nil {
                errs = append(errs, err)
                continue
            }
            inst.Handler.SetVersionContext(vCtx)
            if !inst.Handler.NeedExecute(nil, bc) {
                continue
            }
            if err := inst.Handler.Execute(); err != nil {
                errs = append(errs, fmt.Errorf("%s: %w", compName, err))
                if compRef.Strategy.FailurePolicy == "FailFast" { return kerrors.NewAggregate(errs) }
            }
        }
        if len(errs) > 0 { return kerrors.NewAggregate(errs) }
    }
    return nil
}
```

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

// 接口修改：不传参，返回当前版本
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
1. `BKEClusterReconciler` 获取 `ClusterVersion.Spec.DesiredVersion`。
2. 调用 `manifestStore.GetReleaseImage(desiredVer)` 获取目标 `ReleaseImage`，遍历 `spec.upgrade.components` 填充 `VersionContext.Target`。
3. 获取 `ClusterVersion.Status.CurrentVersion`，调用 `manifestStore.GetReleaseImage(currentVer)` 获取当前 `ReleaseImage`，遍历填充 `VersionContext.Current`。
4. 将构建好的 `VersionContext` 注入到 `BKECluster` 的 `PhaseContext` 中，供所有 Phase 调用 `GetVersionContext()` 使用。

## 5. 平滑升级方案（旧版到新版）
### 5.1 自动迁移方案
1. **部署新 CRD 与控制器（FeatureGate 关闭）**：部署 API 与控制器，`DeclarativeUpgrade=false`，保持原有逻辑。
2. **自动创建 ClusterVersion**：`BKEClusterReconciler` 检测到无 CV 关联时，自动创建 CV 实例，`DesiredVersion` 与 `CurrentVersion` 填充为当前 `BKECluster.Spec.OpenFuyaoVersion`。
3. **开启 FeatureGate 切换**：运维开启开关，后续变更由新流程接管。

### 5.2 手工提前构建方案
为支持更可控的灰度与预检，支持手工提前构建 `ReleaseImage` 与 `UpgradePath`：
1. **手工构建 ReleaseImage**：
   ```bash
   kubectl apply -f ri-v2.6.0.yaml
   ```
   内容包含目标版本的所有组件清单。控制器解析后标记 `Status.Phase=Valid`。
2. **手工构建 UpgradePath**：
   ```bash
   kubectl apply -f up-v2.5.0-to-v2.6.0.yaml
   ```
   定义从旧版本到新版本的升级规则（如 blocked、preCheck 占位）。
3. **关联与触发**：
   创建 `ClusterVersion` 时直接 `spec.releaseImageRef: ri-v2.6.0`，跳过 OCI 拉取阶段，直接使用本地已构建的清单进行升级。适用于离线环境或严格合规场景。

## 6. 异常场景、性能、安全与可扩展性设计
### 6.1 异常场景处理
| 异常 | 处理机制 |
|------|----------|
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
- **敏感数据**：证书、kubeconfig 等通过 K8s Secrets 加密存储，传输全程 mTLS。
- **审计日志**：全链路操作记录至 K8s AuditLog，支持 SIEM 集成。

### 6.4 可扩展性设计
- **水平扩展**：控制器支持 Leader Election + 分片调度（按集群命名空间 Hash），支持多实例部署。
- **插件化 Phase**：通过 `ComponentFactory` 注册机制，第三方组件可动态注入 YAML 或 Go 插件。
- **多架构支持**：OCI 镜像支持 `linux/amd64`, `linux/arm64` 多架构清单，自动匹配节点架构。

## 7. 兼容性 Rule 检查设计
### 7.1 规则语法
支持语义化版本约束：`>=`, `<=`, `>`, `<`, `=`, `!=`, 区间 `>=1.0.0 <2.0.0`。
### 7.2 代码实现
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

## 8. 工作量评估（普通开发者）
| 阶段 | 任务内容 | 工作量 (人天) | 说明 |
|------|---------|:-------------:|------|
| **1. CRD 与 API** | CV/RI/UP/ComponentVersion 定义、Webhook、DeepCopy | 10 | 新手需学习 kubebuilder 脚手架、CRD 验证规则、不可变字段约束 |
| **2. OCI 解析层** | `go-containerregistry` 集成、Config 解析、缓存机制 | 15 | 镜像分层拉取、鉴权配置、离线 fallback 调试耗时较长 |
| **3. 控制器开发** | CV/RI/UP 控制器逻辑、状态机、Annotation 协同机制 | 18 | controller-runtime 调谐循环、Watch 过滤、Reconcile 幂等性设计 |
| **4. DAG 引擎** | 依赖图构建、拓扑排序、FailurePolicy 分支、并发调度 | 14 | 图算法实现、环检测、并发安全与超时控制易出 Bug |
| **5. Phase 适配** | 4 个 Phase YAML 化、`Version()` 接口、`NeedExecute` 改造、Factory 注册 | 10 | 上下文注入调试、旧逻辑兼容、单元测试覆盖 |
| **6. CSP/SAT 求解** | 约束解析、回溯算法实现、兼容性校验集成 | 8 | 语义化版本库使用、约束冲突路径追踪、边界条件处理 |
| **7. 迁移与测试** | 旧版平滑迁移、OCI 预构建流水线、E2E、压测、安全扫描 | 12 | 新手编写集成测试与 Mock 较慢，需反复联调 |
| **总计** | | **87 人天** | 含代码评审、联调缓冲、文档编写 |

## 9. 架构图与流程图
### 9.1 控制器协同架构图
```
用户/CI ──▶ ClusterVersion.Spec.DesiredVersion = "v2.6.0"
               │
               ▼
┌─────────────────────────────────────────────────────────────┐
│ ClusterVersionReconciler                                    │
│  • 拉取 ReleaseImage/UpgradePath OCI 解析为 CR               │
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
│  • 解析 ComponentVersion 依赖 → 构建独立 DAG                │
│  • 按拓扑顺序调用 Phase，注入 VersionContext                │
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
```

## 10. 测试计划与验收标准
| 测试类型 | 覆盖场景 |
|----------|---------|
| **单元测试** | DAG 拓扑排序、兼容性矩阵校验、`NeedExecute` 分支逻辑、OCI 解析 |
| **集成测试** | CV ↔ RI ↔ BKECluster 状态联动、Annotation 触发机制、Phase 注册表映射 |
| **E2E 测试** | 补丁升级、跨版本升级、单组件独立升级、失败中断与回滚、OCI 缺失降级 |
| **兼容性测试** | Feature Gate 关闭时旧流程正常运行；新旧版本混合状态平滑过渡 |
| **压测** | 万级节点并发升级、DAG 构建耗时 <2s、内存泄漏检测 |

**毕业标准**：
- **Alpha**: CRD 注册，控制器可启动，日志验证清单解析与路径查找正确。
- **Beta**: 接管升级流程，与旧 Phase 并行运行，结果对比验证，E2E 通过率 >95%。
- **GA**: 全量切换，移除旧版本硬编码调度逻辑，支持生产环境灰度发布。

## 11. 风险与缓解
| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| OCI 镜像拉取失败或解析错误 | 升级阻塞 | 指数退避重试；本地 ConfigMap fallback；`ReleaseImage` 提前标记 `Invalid` |
| 依赖图存在环路 | DAG 构建死锁 | 拓扑排序前执行环检测算法；超时强制中断并记录 `CycleDetected` 事件 |
| 兼容性校验误报/漏报 | 升级中断或集群不稳定 | 提供 `--skip-compatibility-check` 紧急开关；规则支持热更新；记录详细审计日志 |
| Phase 上下文注入丢失 | 升级决策错误 | `NeedExecute` 增加 nil 保护；单元测试覆盖上下文生命周期；Feature Gate 灰度 |

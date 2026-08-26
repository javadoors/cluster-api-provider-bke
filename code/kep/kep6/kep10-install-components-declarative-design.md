# KEP-10: ReleaseImage 安装组件声明式定义设计

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-10 |
| **标题** | ReleaseImage 安装组件声明式定义与 DAG 驱动安装流程设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-26 |
| **依赖** | KEP-5 声明式升级框架、KEP-6 三层状态机设计、KEP-9 Static Pod 类型设计 |

---

## 1. 摘要

本提案设计 ReleaseImage 安装组件的声明式定义，使安装流程与升级流程统一为 DAG 驱动。当前 ReleaseImage 的 `spec.install.components` 仅包含 `{name, version}` 两个字段，缺少执行模式（inline/manifest/staticpod）、inline handler、依赖关系等声明式元数据；且安装流程完全由硬编码的 `DeployPhases` PhaseFlow 驱动，未消费 ReleaseImage 的安装组件列表。本提案扩展 `ReleaseImageInstallComponent` 结构，新增安装组件目录（`DeclarativeInstallCatalog`）和安装 DAG 构建器，使安装流程也能享受声明式组件管理的优势：依赖管理、并行执行、版本追踪、可观测性。

## 2. 动机

### 2.1 现状问题

| 问题 | 说明 | 影响 |
|------|------|------|
| **安装/升级结构不对称** | `ReleaseImageInstallComponent` 仅 `{name, version}`，`ReleaseImageUpgradeComponent` 有 `inline.handler` | 安装无法声明执行模式 |
| **安装无 DAG** | 安装完全由硬编码 `DeployPhases` 列表驱动 | 无依赖管理、无并行、无断点续传 |
| **安装组件不消费 ReleaseImage** | DeployPhases 从 `BKECluster.Spec` 读取版本，不从 `ReleaseImage.Spec.Install` 读取 | 版本管理脱节 |
| **无安装组件目录** | `DeclarativeUpgradeCatalog` 仅定义升级组件映射，无安装等价物 | 新增安装组件需修改 PhaseFlow 代码 |
| **安装与升级逻辑割裂** | 安装走 PhaseFlow，升级走 DAG，两套机制维护成本高 | 代码重复、行为不一致 |

### 2.2 安装 vs 升级能力对比

| 维度 | 安装（PhaseFlow） | 升级（DAG） |
|------|-------------------|------------|
| **编排方式** | 硬编码 Phase 列表 | ReleaseImage bundle 动态构建 DAG |
| **组件来源** | DeployPhases 构造函数 | `ReleaseImage.Spec.Upgrade.Components` |
| **执行元数据** | 无 | `UpgradeComponentSpec.{Mode, ManifestPath, InlineHandler}` |
| **ReleaseImage 字段** | `Spec.Install` 未被消费 | `Spec.Upgrade.Components` 驱动 DAG |
| **依赖管理** | 隐式 Phase 顺序 | `ComponentVersion.spec.dependencies` + 拓扑排序 |
| **版本决策** | 无（总是执行） | `VersionContext.Decide()` → Skip/Upgrade |
| **组件目录** | 无 | `DeclarativeUpgradeCatalog` |
| **并行执行** | 无（串行 Phase） | 同批次并行（MaxParallel=8） |
| **断点续传** | 无 | `DeclarativeUpgradeStatus` 追踪已完成 |
| **可观测性** | PhaseStatus | `ClusterComponentStatuses` + Events |

### 2.3 设计目标

1. **结构对称**：`ReleaseImageInstallComponent` 与 `ReleaseImageUpgradeComponent` 结构对齐
2. **统一 DAG**：安装流程也通过 DAG 驱动，复用 Scheduler/ExecutorRegistry/VersionContext
3. **统一目录**：`DeclarativeInstallCatalog` 与 `DeclarativeUpgradeCatalog` 结构一致
4. **渐进迁移**：通过 Feature Gate 控制，PhaseFlow 和 DAG 安装路径可并存
5. **向后兼容**：迁移期间 ReleaseImage 可同时包含旧格式和声明式格式的安装组件

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| **CRD 扩展** | `ReleaseImageInstallComponent` 新增 `inline` 字段 |
| **安装组件目录** | `DeclarativeInstallCatalog` 定义安装组件映射 |
| **安装 DAG 构建** | `BuildInstallDAG` 从 ReleaseImage bundle 构建安装 DAG |
| **安装 DAG 执行** | 复用 `Scheduler.ExecuteDAG`，新增 `DecisionInstall` |
| **Feature Gate** | `DeclarativeInstallEnabled` 控制新旧路径切换 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 旧格式 `{name, version}` 的 `ReleaseImageInstallComponent` 必须继续支持 |
| **PhaseFlow 共存** | 迁移期间 PhaseFlow 和 DAG 安装路径可并存，通过 Feature Gate 切换 |
| **复用升级框架** | 安装 DAG 复用 Scheduler、ExecutorRegistry、InlineRunner 等已有组件 |
| **幂等性** | 安装操作必须幂等，支持 Reconcile 重入 |
| **依赖正确性** | 安装 DAG 的依赖关系必须反映实际安装顺序约束 |

### 3.3 非目标

- 不在本文档定义 PreCheck/PostCheck（引用 KEP-5-2）
- 不在本文档定义回滚策略（安装失败不回滚，直接重试）
- 不重写现有 DeployPhases 的 Phase 实现（复用已有代码）

## 4. ReleaseImageInstallComponent 结构扩展

### 4.1 当前结构（不对称）

```go
// 当前：安装组件仅 {name, version}
type ReleaseImageInstallComponent struct {
    Name    string `json:"name,omitempty"`
    Version string `json:"version,omitempty"`
}

// 当前：升级组件有 inline handler
type ReleaseImageUpgradeComponent struct {
    Name    string                     `json:"name,omitempty"`
    Version string                     `json:"version,omitempty"`
    Inline  *ReleaseImageUpgradeInline `json:"inline,omitempty"`
}
```

### 4.2 目标结构（对称）

```go
// api/v1alpha1/releaseimage_types.go

// ReleaseImageInstallComponent 定义安装组件引用 🔄扩展
type ReleaseImageInstallComponent struct {
    // 组件名称（对应 ComponentVersion.Name）
    Name string `json:"name,omitempty"`
    
    // 组件版本（对应 ComponentVersion.Version）
    Version string `json:"version,omitempty"`
    
    // Inline handler 配置（type=inline 时指定）🆕新增
    // 与 ReleaseImageUpgradeInline 结构一致
    Inline *ReleaseImageInstallInline `json:"inline,omitempty"`
}

// ReleaseImageInstallInline 定义安装组件的 inline handler 🆕新增
// 结构与 ReleaseImageUpgradeInline 对称
type ReleaseImageInstallInline struct {
    // Handler 名称（对应 ComponentFactory 注册的 handler）
    // 示例: "EnsureBKEAgent", "EnsureCerts", "EnsureMasterInit"
    Handler string `json:"handler,omitempty"`
    
    // Handler 版本
    Version string `json:"version,omitempty"`
}
```

### 4.3 ReleaseImage YAML 示例（完整安装 + 升级）

```yaml
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ReleaseImage
metadata:
  name: openfuyao-v2.7.0
spec:
  version: "v2.7.0"
  digest: "sha256:..."
  
  # ============================================================
  # 安装组件：定义全新安装时执行的组件列表
  # ============================================================
  install:
    components:
      - name: bkeagent
        version: v2.7.0
        inline:
          handler: EnsureBKEAgent
          version: v1.0.0
      
      - name: nodes-env
        version: v1.0.0
        inline:
          handler: EnsureNodesEnv
          version: v1.0.0
      
      - name: cluster-api-obj
        version: v1.0.0
        inline:
          handler: EnsureClusterAPIObj
          version: v1.0.0
      
      - name: certs
        version: v2.7.0
        inline:
          handler: EnsureCerts
          version: v1.0.0
      
      - name: load-balance
        version: v2.1.4
        inline:
          handler: EnsureLoadBalance
          version: v1.0.0
      
      - name: kubernetes-master
        version: v1.36.0
        inline:
          handler: EnsureMasterInit
          version: v1.0.0
      
      - name: kubernetes-worker
        version: v1.36.0
        inline:
          handler: EnsureWorkerJoin
          version: v1.0.0
      
      - name: kube-proxy
        version: v1.36.0
        # type=yaml, 无需 inline handler
      
      - name: coredns
        version: v1.11.3
        # type=yaml, 无需 inline handler
      
      - name: nodes-postprocess
        version: v1.0.0
        inline:
          handler: EnsureNodesPostProcess
          version: v1.0.0
      
      - name: agent-switch
        version: v1.0.0
        inline:
          handler: EnsureAgentSwitch
          version: v1.0.0
  
  # ============================================================
  # 升级组件：定义版本升级时执行的组件列表
  # ============================================================
  upgrade:
    components:
      - name: pre-upgrade-resources
        version: v1.0.0
        inline:
          handler: EnsurePreUpgradeResources
          version: v1.0.0
      - name: bkeagent
        version: v2.7.0
        inline:
          handler: EnsureAgentUpgrade
          version: v1.0.0
      - name: containerd
        version: v1.7.24
        inline:
          handler: EnsureContainerdUpgrade
          version: v1.0.0
      - name: etcd
        version: v3.5.20
        inline:
          handler: EnsureEtcdUpgrade
          version: v1.0.0
      - name: kubernetes-master
        version: v1.36.0
        inline:
          handler: EnsureMasterUpgrade
          version: v1.0.0
      - name: kubernetes-worker
        version: v1.36.0
        inline:
          handler: EnsureWorkerUpgrade
          version: v1.0.0
      - name: kube-proxy
        version: v1.36.0
      - name: coredns
        version: v1.11.3
```

## 5. 安装组件目录设计

### 5.1 DeclarativeInstallCatalog

```go
// pkg/upgrade/catalog.go

// InstallComponentSpec 定义安装组件规格 🆕新增
// 结构与 UpgradeComponentSpec 对称
type InstallComponentSpec struct {
    // 组件名称（ReleaseImage install.components[].name）
    Name string
    
    // 组件版本
    Version string
    
    // 执行模式: manifest | inline
    Mode InstallExecutionMode
    
    // Manifest 路径 (mode=manifest 时)
    ManifestPath string
    
    // Inline handler 名称 (mode=inline 时)
    InlineHandler string
}

type InstallExecutionMode string

const (
    InstallExecutionManifest InstallExecutionMode = "manifest"
    InstallExecutionInline   InstallExecutionMode = "inline"
)

// DeclarativeInstallCatalog 安装组件目录 🆕新增
// 映射 ReleaseImage install.components 到执行模式
var DeclarativeInstallCatalog = []InstallComponentSpec{
    // inline 模式：复用现有 Phase 实现
    {Name: "bkeagent",          Mode: InstallExecutionInline, InlineHandler: "EnsureBKEAgent"},
    {Name: "nodes-env",         Mode: InstallExecutionInline, InlineHandler: "EnsureNodesEnv"},
    {Name: "cluster-api-obj",   Mode: InstallExecutionInline, InlineHandler: "EnsureClusterAPIObj"},
    {Name: "certs",             Mode: InstallExecutionInline, InlineHandler: "EnsureCerts"},
    {Name: "load-balance",      Mode: InstallExecutionInline, InlineHandler: "EnsureLoadBalance"},
    {Name: "kubernetes-master",  Mode: InstallExecutionInline, InlineHandler: "EnsureMasterInit"},
    {Name: "kubernetes-worker",  Mode: InstallExecutionInline, InlineHandler: "EnsureWorkerJoin"},
    {Name: "nodes-postprocess", Mode: InstallExecutionInline, InlineHandler: "EnsureNodesPostProcess"},
    {Name: "agent-switch",      Mode: InstallExecutionInline, InlineHandler: "EnsureAgentSwitch"},
    
    // manifest 模式：YAML 清单应用
    {Name: "kube-proxy", Mode: InstallExecutionManifest, ManifestPath: "kube-proxy/{version}/component.yaml"},
    {Name: "coredns",    Mode: InstallExecutionManifest, ManifestPath: "coredns/{version}/component.yaml"},
}
```

### 5.2 安装组件与升级组件目录对比

| 组件名称 | 安装 handler | 升级 handler | 执行模式 |
|---------|-------------|-------------|---------|
| bkeagent | `EnsureBKEAgent` | `EnsureAgentUpgrade` | inline |
| nodes-env | `EnsureNodesEnv` | - | inline |
| cluster-api-obj | `EnsureClusterAPIObj` | - | inline |
| certs | `EnsureCerts` | - | inline |
| load-balance | `EnsureLoadBalance` | - | inline |
| kubernetes-master | `EnsureMasterInit` | `EnsureMasterUpgrade` | inline |
| kubernetes-worker | `EnsureWorkerJoin` | `EnsureWorkerUpgrade` | inline |
| nodes-postprocess | `EnsureNodesPostProcess` | - | inline |
| agent-switch | `EnsureAgentSwitch` | - | inline |
| kube-proxy | (manifest) | (manifest) | manifest |
| coredns | (manifest) | (manifest) | manifest |
| pre-upgrade-resources | - | `EnsurePreUpgradeResources` | inline |
| containerd | - | `EnsureContainerdUpgrade` | inline |
| etcd | - | `EnsureEtcdUpgrade` | inline |

### 5.3 ComponentFactory 注册扩展

```go
// pkg/componentfactory/registry.go

func registerInstallHandlers() {
    // 安装 handler 注册（复用现有 Phase 构造函数）
    RegisterInlineHandler("EnsureBKEAgent",       v1alpha1.InlineHandlerVersion, phases.NewEnsureBKEAgent)
    RegisterInlineHandler("EnsureNodesEnv",       v1alpha1.InlineHandlerVersion, phases.NewEnsureNodesEnv)
    RegisterInlineHandler("EnsureClusterAPIObj",  v1alpha1.InlineHandlerVersion, phases.NewEnsureClusterAPIObj)
    RegisterInlineHandler("EnsureCerts",          v1alpha1.InlineHandlerVersion, phases.NewEnsureCerts)
    RegisterInlineHandler("EnsureLoadBalance",    v1alpha1.InlineHandlerVersion, phases.NewEnsureLoadBalance)
    RegisterInlineHandler("EnsureMasterInit",     v1alpha1.InlineHandlerVersion, phases.NewEnsureMasterInit)
    RegisterInlineHandler("EnsureWorkerJoin",     v1alpha1.InlineHandlerVersion, phases.NewEnsureWorkerJoin)
    RegisterInlineHandler("EnsureNodesPostProcess", v1alpha1.InlineHandlerVersion, phases.NewEnsureNodesPostProcess)
    RegisterInlineHandler("EnsureAgentSwitch",    v1alpha1.InlineHandlerVersion, phases.NewEnsureAgentSwitch)
    
    // 升级 handler 已注册（现有）
    // RegisterInlineHandler("EnsureEtcdUpgrade", ...)
    // RegisterInlineHandler("EnsureMasterUpgrade", ...)
    // ...
}
```

## 6. 安装 DAG 构建设计

### 6.1 VersionContext 扩展

```go
// pkg/upgrade/context.go

// Decision 扩展 🆕新增 DecisionInstall
type Decision string

const (
    DecisionSkip    Decision = "Skip"
    DecisionUpgrade Decision = "Upgrade"
    DecisionInstall Decision = "Install"  // 🆕新增
)

// Decide 扩展：安装场景判断
func Decide(vc *VersionContext, name string) Decision {
    if vc == nil {
        return DecisionUpgrade  // nil VC = 不阻塞
    }
    
    // 安装场景：current 为空，target 有值
    if vc.Current[name] == "" && vc.Target[name] != "" {
        return DecisionInstall
    }
    
    // 升级场景：current != target
    if vc.Current[name] == vc.Target[name] || vc.Target[name] == "" {
        return DecisionSkip
    }
    return DecisionUpgrade
}

// NeedsExecution 扩展
func NeedsExecution(vc *VersionContext, name string) bool {
    return Decide(vc, name) != DecisionSkip
}
```

### 6.2 安装 DAG 构建器

```go
// pkg/upgrade/bundle.go

// InstallComponentsFromBundle 从 ReleaseImage bundle 提取安装组件
func InstallComponentsFromBundle(bundle *releasemanifest.Bundle) ([]apiv1.ReleaseImageInstallComponent, error) {
    if bundle.Release.Spec.Install == nil {
        return nil, fmt.Errorf("release image has no install components")
    }
    components := bundle.Release.Spec.Install.Components
    if len(components) == 0 {
        return nil, fmt.Errorf("release image install components is empty")
    }
    return components, nil
}

// BuildInstallDAGFromBundle 从 bundle 构建安装 DAG 🆕新增
func BuildInstallDAGFromBundle(
    bundle *releasemanifest.Bundle,
    resolve topology.DependencyResolver,
) (*topology.UpgradeDAG, error) {
    // 1. 提取安装组件
    installComponents, err := InstallComponentsFromBundle(bundle)
    if err != nil {
        return nil, err
    }
    
    // 2. 转换为 ComponentNode（复用 UpgradeComponent 结构）
    var components []topology.ReleaseImageUpgradeComponent
    for _, ic := range installComponents {
        comp := topology.ReleaseImageUpgradeComponent{
            Name:    ic.Name,
            Version: ic.Version,
        }
        if ic.Inline != nil {
            comp.Inline = &topology.ReleaseImageUpgradeInline{
                Handler: ic.Inline.Handler,
                Version: ic.Inline.Version,
            }
        }
        components = append(components, comp)
    }
    
    // 3. 复用 BuildUpgradeDAG（同一构建逻辑）
    return topology.BuildUpgradeDAG(components, resolve)
}
```

### 6.3 安装 VersionContext 构建

```go
// pkg/upgrade/build_release.go

// BuildVersionContextForInstall 为安装场景构建 VersionContext 🆕新增
func BuildVersionContextForInstall(
    targetBundle *releasemanifest.Bundle,
) *VersionContext {
    vc := NewVersionContext()
    
    // 安装场景：Current 全部为空（全新安装），Target 来自 ReleaseImage
    // install.components 填充 Target
    if targetBundle.Release.Spec.Install != nil {
        for _, comp := range targetBundle.Release.Spec.Install.Components {
            vc.SetTarget(comp.Name, comp.Version)
        }
    }
    
    // upgrade.components 也填充 Target（覆盖同名 install 条目）
    if targetBundle.Release.Spec.Upgrade != nil {
        for _, comp := range targetBundle.Release.Spec.Upgrade.Components {
            vc.SetTarget(comp.Name, comp.Version)
        }
    }
    
    // Current 全部为空 → Decide 返回 DecisionInstall
    return vc
}
```

## 7. 安装 DAG 执行设计

### 7.1 执行入口

```go
// controllers/capbke/bkecluster_controller.go

func (r *BKEClusterReconciler) executePhaseFlow(ctx, phaseCtx, oldCluster, newCluster) {
    // 现有：DAG 升级路径
    if r.shouldUseDeclarativeUpgrade(newCluster) {
        r.executeUpgradeDAG(...)
        ...
    }
    
    // 🆕新增：DAG 安装路径
    if r.shouldUseDeclarativeInstall(newCluster) {
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
        // 安装 DAG 完成后跳过 DeployPhases
        return
    }
    
    // 现有：PhaseFlow 路径（Legacy）
    flow := phases.NewPhaseFlow(phaseCtx)
    ...
}
```

**PhaseFlow 路径（Legacy）的适用场景**：

以下场景仍会走 Legacy PhaseFlow 路径，不走 DAG 安装路径：

| 场景 | 原因 | 说明 |
|------|------|------|
| **Feature Gate 未启用** | `DeclarativeInstallEnabled = false`（默认） | 迁移期间默认关闭，确保生产稳定；正式启用后此场景消失 |
| **ReleaseImage 无 inline handler** | `install.components[].inline` 字段为空 | 旧格式 ReleaseImage 仅有 `{name, version}`，DAG 无法分发执行器；需升级 ReleaseImage 格式后才能走 DAG |
| **无 install-ready annotation** | ClusterVersionReconciler 未设置 `install-ready` | 仅当 ReleaseImage Status.Phase=Valid 且 ClusterVersion 判定安装就绪时才设置 annotation |
| **纳管已有集群** | `BKECluster.Spec.Manage = true`（纳管模式） | 纳管现有集群不走标准安装流程，PhaseFlow 中的 `EnsureClusterManage` 处理纳管逻辑 |
| **集群扩容（新增节点）** | `EnsureMasterJoin` / `EnsureWorkerJoin` | 扩容时只执行部分 Phase（Join），非完整安装；DAG 安装路径面向全新安装，扩容仍走 PhaseFlow 中的 Scale Phase |
| **集群删除/重置** | `BKECluster.Spec.Reset = true` 或 `DeletionTimestamp` 非空 | 删除/重置走 `DeletePhases`，与安装 DAG 无关 |
| **DryRun 模式** | `BKECluster.Spec.DryRun = true` | DryRun 走 PhaseFlow 中的 `EnsureDryRun`，不实际执行安装 |
| **集群暂停** | `BKECluster.Spec.Pause = true` | 暂停状态下不执行任何操作，走 PhaseFlow 中的 `EnsurePaused` |

> **注意**：扩容场景（新增 Master/Worker 节点）虽然不走 DAG 安装路径，但未来可考虑将扩容也纳入 DAG 驱动（如 `kubernetes-master` 组件的 `DecisionInstall` 触发 `EnsureMasterJoin` handler），作为后续优化方向。

```go
// shouldUseDeclarativeInstall 判断是否使用 DAG 安装路径 🆕新增
func (r *BKEClusterReconciler) shouldUseDeclarativeInstall(bkeCluster *bkev1beta1.BKECluster) bool {
    // Feature Gate 控制
    if !featuregate.DeclarativeInstallEnabled.Enabled() {
        return false
    }
    // 仅在全新安装时启用（Status.Phase 为空或 Init）
    if bkeCluster.Status.Phase != "" && bkeCluster.Status.Phase != bkev1beta1.PhaseInit {
        return false
    }
    // 检查是否有安装 annotation（由 ClusterVersionReconciler 设置）
    _, ok := annotation.HasAnnotation(bkeCluster, annotation.InstallReadyAnnotationKey)
    return ok
}
```

### 7.2 executeInstallDAG 实现

```go
// controllers/capbke/bkecluster_install_dag.go 🆕新增

func (r *BKEClusterReconciler) executeInstallDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 1. 解析目标 ReleaseImage
    releaseImage, bundle, err := r.resolveInstallBundle(ctx, newCluster)
    
    // 2. 构建安装 VersionContext（Current 为空，Target 来自 bundle）
    vc := upgrade.BuildVersionContextForInstall(bundle)
    phaseCtx.SetVersionContext(vc)
    
    // 3. 同步目标版本到 BKECluster.Spec
    upgrade.ApplyVersionContextTargetsToClusterSpec(vc, newCluster)
    
    // 4. 构建安装 DAG
    dag, err := upgrade.BuildInstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    
    // 5. 构建 ComponentFactory（注册安装 handler）
    factory := componentfactory.NewFactoryFromBundle(bundle)
    // 额外注册安装 handler
    factory.RegisterInstallHandlers()
    
    // 6. 构建 Scheduler（复用升级框架）
    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:    NewInlinePhaseRunnerAdapter(phaseCtx, &PhaseRunner{Factory: factory}),
        ManifestStore:   manifest.NewBundleStore(bundle),
        ManifestApplier: r.ManifestApplier,
        CVStore:         manifest.NewBundleStore(bundle),
        MaxParallelPerBatch: 8,
    })
    
    // 7. 构建 ExecutionContext
    execCtx := buildExecutionContext(ctx, r.Client, newCluster, vc)
    
    // 8. 执行 DAG
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        return err
    }
    
    // 9. 安装完成：更新状态
    newCluster.Status.Phase = bkev1beta1.PhaseReady
    newCluster.Status.ClusterStatus = bkev1beta1.ClusterStatusReady
    
    return nil
}
```

### 7.3 安装 DAG 结构

```
安装 DAG（基于 ReleaseImage v2.7.0 install.components 构建）:

依赖关系来自 ComponentVersion.spec.dependencies:

Batch 1: [bkeagent]                  ← 无依赖，最先执行
    └─ inline: EnsureBKEAgent → SSH 推送 bkeagent 二进制

Batch 2: [nodes-env]                 ← 依赖 bkeagent
    └─ inline: EnsureNodesEnv → 节点环境准备（runc/lxcfs/nfs-utils 等）

Batch 3: [certs]                     ← 依赖 nodes-env
    └─ inline: EnsureCerts → 证书生成

Batch 4: [cluster-api-obj, load-balance]  ← 依赖 certs，并行执行
    ├─ inline: EnsureClusterAPIObj → CAPI 对象创建
    └─ inline: EnsureLoadBalance → haproxy/keepalived 部署

Batch 5: [kubernetes-master]         ← 依赖 certs + load-balance
    └─ inline: EnsureMasterInit → kubeadm init + 控制面启动

Batch 6: [kubernetes-worker]         ← 依赖 kubernetes-master（倾斜策略）
    └─ inline: EnsureWorkerJoin → kubeadm join

Batch 7: [kube-proxy, coredns]       ← 依赖 kubernetes-master，并行执行
    ├─ manifest: YamlInstaller Apply → kube-proxy DaemonSet
    └─ manifest: YamlInstaller Apply → coredns Deployment

Batch 8: [nodes-postprocess, agent-switch]  ← 依赖 kubernetes-worker，并行执行
    ├─ inline: EnsureNodesPostProcess → 后置脚本
    └─ inline: EnsureAgentSwitch → Agent 监听切换
```

> **注意**：实际 DAG 结构由 `ComponentVersion.spec.dependencies` 决定。上图为典型配置。

## 8. 部署 Phase 与安装组件映射

### 8.1 DeployPhases → 安装组件映射

| DeployPhase | 安装组件名 | 执行模式 | Inline Handler | 说明 |
|-------------|-----------|---------|---------------|------|
| `EnsureBKEAgent` | bkeagent | inline | `EnsureBKEAgent` | SSH 推送 Agent |
| `EnsureNodesEnv` | nodes-env | inline | `EnsureNodesEnv` | 节点环境准备 |
| `EnsureClusterAPIObj` | cluster-api-obj | inline | `EnsureClusterAPIObj` | CAPI 对象 |
| `EnsureCerts` | certs | inline | `EnsureCerts` | 证书生成 |
| `EnsureLoadBalance` | load-balance | inline | `EnsureLoadBalance` | HA 负载均衡 |
| `EnsureMasterInit` | kubernetes-master | inline | `EnsureMasterInit` | Master 初始化 |
| `EnsureMasterJoin` | kubernetes-master | inline | `EnsureMasterInit` | Master 加入（复用） |
| `EnsureWorkerJoin` | kubernetes-worker | inline | `EnsureWorkerJoin` | Worker 加入 |
| `EnsureAddonDeploy` | (coredns) | manifest | - | 附加组件部署 |
| `EnsureNodesPostProcess` | nodes-postprocess | inline | `EnsureNodesPostProcess` | 后置脚本 |
| `EnsureAgentSwitch` | agent-switch | inline | `EnsureAgentSwitch` | Agent 切换 |

### 8.2 安装 vs 升级组件差异

| 组件 | 安装 handler | 升级 handler | 差异说明 |
|------|-------------|-------------|---------|
| **bkeagent** | `EnsureBKEAgent` | `EnsureAgentUpgrade` | 安装=首次推送；升级=SSH 推送新版本 |
| **kubernetes-master** | `EnsureMasterInit` | `EnsureMasterUpgrade` | 安装=kubeadm init；升级=kubeadm upgrade |
| **kubernetes-worker** | `EnsureWorkerJoin` | `EnsureWorkerUpgrade` | 安装=kubeadm join；升级=kubeadm upgrade |
| **containerd** | (包含在 nodes-env) | `EnsureContainerdUpgrade` | 安装=环境准备含 containerd；升级=独立 ENV 命令 |
| **etcd** | (包含在 MasterInit) | `EnsureEtcdUpgrade` | 安装=kubeadm init 含 etcd；升级=独立 etcd 升级 |
| **certs** | `EnsureCerts` | - | 仅安装时生成证书 |
| **load-balance** | `EnsureLoadBalance` | - | 仅安装时配置 HA |
| **nodes-env** | `EnsureNodesEnv` | - | 仅安装时准备环境 |
| **pre-upgrade-resources** | - | `EnsurePreUpgradeResources` | 仅升级时预创建资源 |

## 9. Feature Gate 与迁移策略

### 9.1 Feature Gate 设计

```go
// pkg/featuregate/features.go

var (
    // DeclarativeInstallEnabled 控制 DAG 安装路径是否启用
    // false: 使用 PhaseFlow 安装（默认）
    // true: 使用 DAG 安装
    DeclarativeInstallEnabled = featuregate.NewFeature()
)
```

### 9.2 迁移阶段

| 阶段 | 目标 | 说明 | Feature Gate |
|------|------|------|-------------|
| **Phase 1** | 结构扩展 | 扩展 `ReleaseImageInstallComponent`，新增 `inline` 字段 | 不启用 |
| **Phase 2** | 安装 DAG 实现 | 实现 `BuildInstallDAG`、`executeInstallDAG`、`DeclarativeInstallCatalog` | 灰度启用 |
| **Phase 3** | 验证与切换 | 测试环境验证，逐步切换到 DAG 安装路径 | 正式启用 |
| **Phase 4** | 移除 PhaseFlow | 移除 DeployPhases 硬编码列表，安装完全 DAG 驱动 | 移除 Feature Gate |

### 9.3 向后兼容

1. **旧格式兼容**：`ReleaseImageInstallComponent` 的 `inline` 字段是 `omitempty`，旧格式 `{name, version}` 仍然有效
2. **PhaseFlow 共存**：`DeclarativeInstallEnabled` 未启用时，继续使用 PhaseFlow
3. **混合模式**：ReleaseImage 可同时包含有 `inline` 和无 `inline` 的安装组件
4. **ClusterVersionReconciler**：安装时设置 `cvo.openfuyao.cn/install-ready` annotation 触发 DAG 路径

### 9.4 Legacy PhaseFlow 完全移除方案

当迁移到 Phase 4 时，需要完全移除 Legacy PhaseFlow 路径。以下针对 7.1 节中列出的每个 Legacy 场景，给出 DAG 化的完整方案：

#### 9.4.1 场景覆盖总览

| Legacy 场景 | DAG 化方案 | 移除条件 |
|------------|-----------|---------|
| Feature Gate 未启用 | 移除 Feature Gate，DAG 成为唯一路径 | Phase 4 |
| ReleaseImage 无 inline handler | 强制 ReleaseImage 包含 inline 字段 | Phase 4 |
| 无 install-ready annotation | ClusterVersionReconciler 始终设置 annotation | Phase 2 |
| 纳管已有集群 | 新增 `manage` 组件到安装 DAG | Phase 4 |
| 集群扩容（新增节点） | 新增 `scale-master` / `scale-worker` 组件到 DAG | Phase 4 |
| 集群删除/重置 | 新增 `delete` DAG（逆序卸载） | Phase 4 |
| DryRun 模式 | DAG 执行器支持 DryRun 标记 | Phase 3 |
| 集群暂停 | DAG 前置检查 `BKECluster.Spec.Pause` | Phase 3 |

#### 9.4.2 纳管已有集群 DAG 化

**当前实现**：`EnsureClusterManage` Phase 处理纳管逻辑（检测已有集群组件版本、写入 BKECluster.Status）。

**DAG 化方案**：

```yaml
# ReleaseImage install.components 新增 manage 组件
install:
  components:
    - name: manage
      version: v1.0.0
      inline:
        handler: EnsureClusterManage
        version: v1.0.0
```

```go
// DeclarativeInstallCatalog 新增
{ Name: "manage", Mode: InstallExecutionInline, InlineHandler: "EnsureClusterManage" }
```

**VersionContext 特殊处理**：纳管场景下 Current 不为空（从已有集群探测版本），Target 来自 ReleaseImage：

```go
// BuildVersionContextForManage 为纳管场景构建 VersionContext
func BuildVersionContextForManage(
    targetBundle *releasemanifest.Bundle,
    detectedVersions map[string]string,  // 从已有集群探测的版本
) *VersionContext {
    vc := NewVersionContext()
    // Current 来自探测结果
    for name, ver := range detectedVersions {
        vc.SetCurrent(name, ver)
    }
    // Target 来自 ReleaseImage
    for _, comp := range targetBundle.Release.Spec.Install.Components {
        vc.SetTarget(comp.Name, comp.Version)
    }
    return vc
}
```

**触发机制**：`BKECluster.Spec.Manage = true` 时，ClusterVersionReconciler 设置 `install-ready` annotation 触发 DAG，DAG 中 `manage` 组件作为第一个 Batch 执行（探测版本 + 填充 Current），后续组件通过 `VersionContext.Decide()` 判断是否需要执行（已有正确版本的跳过）。

#### 9.4.3 集群扩容 DAG 化

**当前实现**：`EnsureMasterJoin` / `EnsureWorkerJoin` Phase 处理新增节点。

**DAG 化方案**：将扩容纳入安装 DAG，通过 VersionContext 的 `DecisionInstall` 触发：

```yaml
# ReleaseImage install.components 已有 kubernetes-master / kubernetes-worker
# 扩容时新增节点的 VersionContext.Current 为空 → DecisionInstall
```

**扩容 DAG 结构**：

```
扩容 DAG（新增 Master 节点）:

Batch 1: [bkeagent]              ← 新节点需要先安装 Agent
    └─ inline: EnsureBKEAgent → SSH 推送 bkeagent

Batch 2: [nodes-env]            ← 新节点需要环境准备
    └─ inline: EnsureNodesEnv

Batch 3: [kubernetes-master]    ← 新节点 kubeadm join
    └─ inline: EnsureMasterInit (幂等：已有 Master 跳过，新节点执行 join)

Batch 4: [kube-proxy, coredns]   ← DaemonSet 自动调度到新节点
    └─ manifest: YamlInstaller Apply
```

**关键设计**：
- `EnsureMasterInit` handler 需区分"首个 Master（init）"和"后续 Master（join）"，通过检查已有 Master 数量判断
- 已有节点的组件 VersionContext.Current 已有值且与 Target 一致 → `DecisionSkip`，跳过执行
- 新增节点的组件 VersionContext.Current 为空 → `DecisionInstall`，执行安装
- 扩容不再走独立的 Scale Phase，而是复用安装 DAG（VersionContext 自动过滤已完成的节点）

#### 9.4.4 集群删除/重置 DAG 化

**当前实现**：`EnsureDeleteOrReset` Phase 处理删除/重置。

**DAG 化方案**：构建卸载 DAG（安装 DAG 逆序），逐组件卸载：

```go
// BuildUninstallDAGFromBundle 构建卸载 DAG（逆序）
func BuildUninstallDAGFromBundle(
    bundle *releasemanifest.Bundle,
    resolve topology.DependencyResolver,
) (*topology.UpgradeDAG, error) {
    // 复用 BuildInstallDAGFromBundle，然后逆序
    dag, err := BuildInstallDAGFromBundle(bundle, resolve)
    if err != nil {
        return nil, err
    }
    // 逆序 DAG（依赖关系反转）
    return dag.Reverse(), nil
}
```

**卸载 DAG 结构**：

```
卸载 DAG（安装 DAG 逆序）:

Batch 1: [agent-switch, nodes-postprocess]   ← 先停止 Agent 监听 + 后置清理
    ├─ inline: EnsureAgentSwitch → 切回原 Agent
    └─ inline: EnsureNodesPostProcess → 清理

Batch 2: [kube-proxy, coredns]               ← 卸载附加组件
    ├─ manifest: YamlInstaller Delete
    └─ manifest: YamlInstaller Delete

Batch 3: [kubernetes-worker]                 ← 清理 Worker 节点
    └─ inline: kubeadm reset (worker)

Batch 4: [kubernetes-master]                 ← 清理 Master 节点
    └─ inline: kubeadm reset (master)

Batch 5: [load-balance]                      ← 清理 HA
    └─ inline: 删除 haproxy/keepalived

Batch 6: [certs]                            ← 清理证书
    └─ inline: 删除证书

Batch 7: [cluster-api-obj]                   ← 清理 CAPI 对象

Batch 8: [bkeagent]                          ← 最后卸载 Agent
    └─ inline: 停止并删除 bkeagent
```

**触发机制**：`BKECluster.Spec.Reset = true` 或 `DeletionTimestamp` 非空时，构建卸载 DAG 执行。

#### 9.4.5 DryRun 模式 DAG 化

**当前实现**：`EnsureDryRun` Phase 处理 DryRun。

**DAG 化方案**：在 ExecutionContext 中传递 `DryRun` 标记，各执行器检查标记后仅打印不实际执行：

```go
// ExecutionContext 新增 DryRun 字段
type ExecutionContext struct {
    // ... 现有字段 ...
    DryRun bool  // 🆕新增
}

// Scheduler 执行时检查 DryRun
func (s *Scheduler) executeComponent(ctx, node, execCtx) error {
    if execCtx.DryRun {
        // 仅打印将要执行的操作，不实际执行
        log.Info("DryRun: would execute component", "name", node.Name, "version", node.Version)
        return nil
    }
    // 实际执行
    ...
}
```

**触发机制**：`BKECluster.Spec.DryRun = true` 时，`executeInstallDAG` 中设置 `execCtx.DryRun = true`，DAG 照常构建和遍历但各组件仅打印不执行。

#### 9.4.6 集群暂停 DAG 化

**当前实现**：`EnsurePaused` Phase 处理暂停。

**DAG 化方案**：在 `shouldUseDeclarativeInstall` 中增加暂停检查，暂停时不构建 DAG：

```go
func (r *BKEClusterReconciler) shouldUseDeclarativeInstall(bkeCluster *bkev1beta1.BKECluster) bool {
    // ... 现有检查 ...
    
    // 🆕新增：暂停检查
    if bkeCluster.Spec.Pause {
        return false  // 暂停时不执行任何操作
    }
    
    return ok
}
```

**说明**：暂停场景不需要 DAG 化——暂停的语义是"不执行任何操作"，DAG 路径和 PhaseFlow 路径都需要跳过执行。移除 PhaseFlow 后，暂停检查仍保留在 `shouldUseDeclarativeInstall` 中作为前置判断。

#### 9.4.7 移除后的执行入口

完全移除 Legacy PhaseFlow 后，执行入口简化为：

```go
func (r *BKEClusterReconciler) reconcileCluster(ctx, phaseCtx, oldCluster, newCluster) {
    // 场景判断（无 Legacy 回退）
    switch {
    case isDeleteOrReset(newCluster):
        // 卸载 DAG
        r.executeUninstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    
    case isPaused(newCluster):
        // 暂停：不执行
        return
    
    case isDryRun(newCluster):
        // DryRun DAG
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster, WithDryRun())
    
    case isScale(newCluster):
        // 扩容 DAG（复用安装 DAG，VersionContext 自动过滤）
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    
    case isManage(newCluster):
        // 纳管 DAG
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster, WithManage())
    
    case r.shouldUseDeclarativeUpgrade(newCluster):
        // 升级 DAG
        r.executeUpgradeDAG(...)
    
    default:
        // 全新安装 DAG
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    }
    
    // 不再有 PhaseFlow 回退
}
```

#### 9.4.8 移除工作量

| 任务 | 说明 | 工作量（人天） |
|------|------|---------------|
| 纳管 DAG 化 | `manage` 组件 + `BuildVersionContextForManage` | 2 |
| 扩容 DAG 化 | `EnsureMasterInit` 幂等改造 + VersionContext 过滤 | 2 |
| 删除/重置 DAG 化 | `BuildUninstallDAGFromBundle` + 逆序执行 | 3 |
| DryRun DAG 化 | `ExecutionContext.DryRun` + 各执行器适配 | 1 |
| 暂停检查迁移 | `shouldUseDeclarativeInstall` 增加暂停检查 | 0.5 |
| 执行入口重写 | `reconcileCluster` 场景分发 | 1 |
| PhaseFlow 代码清理 | 移除 `DeployPhases` / `PhaseFlow` / `PhaseStatus` | 2 |
| 回归测试 | 所有场景 E2E 测试 | 5 |
| **小计** | - | **16.5 人天** |

> **注意**：此工作量叠加在 Phase 1-3（27 人天）之上，Legacy 完全移除的总工作量为 27 + 16.5 = 43.5 人天。

## 10. 可观测性

### 10.1 安装状态追踪

```bash
# 查询安装 DAG 执行进度
kubectl get bkecluster my-cluster -o jsonpath='{.status.declarativeUpgrade}'
# 输出:
# {
#   "targetVersion": "v2.7.0",
#   "completed": ["bkeagent", "nodes-env", "certs", "cluster-api-obj"],
#   "lastError": ""
# }

# 查询组件安装状态
kubectl get bkecluster my-cluster -o jsonpath='{.status.clusterComponentStatuses}'
# 输出:
# {
#   "bkeagent": {"phase": "Installed", "version": "v2.7.0"},
#   "certs": {"phase": "Installed", "version": "v2.7.0"},
#   "kubernetes-master": {"phase": "Pending", "version": "v1.36.0"}
# }
```

### 10.2 事件与指标

| 类型 | 来源 | 说明 |
|------|------|------|
| **InstallStarted/Completed/Failed** | BKECluster Controller | 安装操作事件 |
| **ComponentInstalled/Failed** | ComponentStatusUpdater | 组件安装事件 |
| **bke_cluster_phase** | Prometheus | 集群状态指标 |
| **bke_component_phase** | Prometheus | 组件状态指标 |

## 11. 工作量评估

### 11.1 开发工作量

| 模块 | 任务 | 工作量（人天） |
|------|------|---------------|
| **CRD 扩展** | `ReleaseImageInstallComponent` 新增 `inline` 字段 + deepcopy + webhook | 1 |
| **安装组件目录** | `DeclarativeInstallCatalog` + `InstallComponentSpec` | 1 |
| **ComponentFactory 注册** | 注册安装 handler 到 factory | 1 |
| **VersionContext 扩展** | 新增 `DecisionInstall` + `BuildVersionContextForInstall` | 1.5 |
| **安装 DAG 构建** | `BuildInstallDAGFromBundle` + `InstallComponentsFromBundle` | 2 |
| **executeInstallDAG** | 安装 DAG 执行入口 + `shouldUseDeclarativeInstall` | 2 |
| **安装 annotation 机制** | `install-ready` annotation + ClusterVersionReconciler 适配 | 1 |
| **Feature Gate** | `DeclarativeInstallEnabled` 实现 | 0.5 |
| **ComponentVersion 依赖定义** | 为安装组件编写 `spec.dependencies` | 2 |
| **ReleaseImage 适配** | 为 v2.7.0 ReleaseImage 补充 install.components.inline | 1 |
| **小计** | - | **13 人天** |

### 11.2 测试工作量

| 测试类型 | 测试内容 | 工作量（人天） |
|---------|---------|---------------|
| **单元测试** | DAG 构建、VersionContext、Catalog | 2 |
| **集成测试** | 全新安装流程 | 5 |
| **E2E 测试** | 完整安装 → 升级流程 | 5 |
| **回归测试** | PhaseFlow 路径回归 | 2 |
| **小计** | - | **14 人天** |

### 11.3 Legacy 完全移除工作量

| 任务 | 说明 | 工作量（人天） |
|------|------|---------------|
| 纳管 DAG 化 | `manage` 组件 + `BuildVersionContextForManage` | 2 |
| 扩容 DAG 化 | `EnsureMasterInit` 幂等改造 + VersionContext 过滤 | 2 |
| 删除/重置 DAG 化 | `BuildUninstallDAGFromBundle` + 逆序执行 | 3 |
| DryRun DAG 化 | `ExecutionContext.DryRun` + 执行器适配 | 1 |
| 暂停检查迁移 | `shouldUseDeclarativeInstall` 增加暂停检查 | 0.5 |
| 执行入口重写 | `reconcileCluster` 场景分发 | 1 |
| PhaseFlow 代码清理 | 移除 `DeployPhases` / `PhaseFlow` / `PhaseStatus` | 2 |
| 回归测试 | 所有场景 E2E 测试 | 5 |
| **小计** | - | **16.5 人天** |

### 11.4 总工作量汇总

| 类别 | 工作量（人天） |
|------|---------------|
| **开发**（Phase 1-3） | 13 |
| **测试**（Phase 1-3） | 14 |
| **Legacy 移除**（Phase 4） | 16.5 |
| **总计** | **43.5** |

## 12. 风险与缓解措施

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| **安装 DAG 依赖不正确** | 组件安装顺序错误 | 中 | 充分测试依赖关系，与 PhaseFlow 顺序对齐 |
| **PhaseFlow 回归** | 新增 DAG 路径影响现有安装 | 低 | Feature Gate 控制，默认不启用 |
| **ComponentVersion 缺失** | 安装组件无依赖定义 | 中 | 补充所有安装组件的 `spec.dependencies` |
| **新旧路径行为不一致** | DAG 安装与 PhaseFlow 安装结果不同 | 中 | 对比测试，确保行为一致 |
| **MasterJoin 复用 MasterInit handler** | Master 加入使用 MasterInit 逻辑可能不匹配 | 低 | 验证 MasterInit handler 对已有 Master 的幂等性 |

---

## 附录

### A. 参考文档

1. [KEP-5 声明式升级框架](kep5/kep5.md)
2. [KEP-6 三层状态机设计](kep6-state-machine-v5.md)
3. [KEP-9 Static Pod 类型设计](kep9-staticpod-upgrade-framework.md)
4. [声明式集群版本升级方案-支持二进制与 Helm 组件](声明式集群版本升级方案-支持二进制与 Helm 组件.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **ReleaseImageInstallComponent** | ReleaseImage 中安装组件引用，扩展后包含 `inline` handler |
| **DeclarativeInstallCatalog** | 安装组件目录，映射组件名到执行模式（inline/manifest） |
| **BuildInstallDAGFromBundle** | 从 ReleaseImage bundle 构建安装 DAG |
| **DecisionInstall** | VersionContext 决策：current 为空且 target 有值时触发安装 |
| **DeclarativeInstallEnabled** | Feature Gate，控制 DAG 安装路径是否启用 |
| **install-ready annotation** | `cvo.openfuyao.cn/install-ready`，触发 DAG 安装路径 |

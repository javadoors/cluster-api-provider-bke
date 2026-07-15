# 声明式集群安装 DAG 详细设计

## 目录

1. [概述](#1-概述)
   - 1.1 设计目标
   - 1.2 设计范围
   - 1.3 问题分析
   - 1.4 术语表
2. [现状分析](#2-现状分析)
   - 2.1 安装与升级的架构不对称
   - 2.2 现有安装路径（PhaseFlow）
   - 2.3 现有升级路径（DAG）
   - 2.4 ReleaseImage install.components 使用现状
3. [共享设计（两方案通用）](#3-共享设计两方案通用)
   - 3.1 InstallComponent 富化机制（Bundle enrich）
   - 3.2 安装 DAG 构建（复用 BuildUpgradeDAG）
   - 3.3 VersionContext 扩展（HasCurrent + BuildVersionContextForInstall）
   - 3.4 进度跟踪（复用 DeclarativeUpgradeStatus）
   - 3.5 Feature Gate 集成
4. [方案 A：仅组件层](#4-方案-a仅组件层)
   - 4.1 架构定位
   - 4.2 控制器流程
   - 4.3 install.components 内容约束
   - 4.4 与 PhaseFlow 的衔接
   - 4.5 PhaseFlow 分段执行扩展
5. [方案 B：全量替换 PhaseFlow](#5-方案-b全量替换-phaseflow)
   - 5.1 架构定位
   - 5.2 安装 Inline Handler 注册
   - 5.3 安装 Catalog（组件依赖图）
   - 5.4 install.components 内容
   - 5.5 控制器流程
   - 5.6 状态报告映射
6. [两方案对比与推荐](#6-两方案对比与推荐)
   - 6.1 对比总览
   - 6.2 技术风险分析
   - 6.3 推荐路径
7. [实施任务清单](#7-实施任务清单)
   - 7.1 共享任务
   - 7.2 方案 A 专属任务
   - 7.3 方案 B 专属任务
   - 7.4 测试任务
8. [测试设计](#8-测试设计)
   - 8.1 单元测试
   - 8.2 集成测试
   - 8.3 E2E 测试
9. [附录](#9-附录)
   - 9.1 参考文档
   - 9.2 文件索引

## 1. 概述

### 1.1 设计目标

本设计文档补齐「声明式集群版本升级方案-支持 Helm 组件」中安装 DAG 的缺失设计。主文档第 9.1 节安装流程图引用了 `BuildInstallDAG(releaseImage)`，但缺乏对应的详细设计章节、函数定义和控制器接线。

具体目标：

- **安装 DAG 构建函数**：对称于升级侧 `BuildDAGFromBundle`（`pkg/upgrade/bundle.go:24`），从 `ReleaseImage.Spec.Install.Components` 构建 DAG
- **VersionContext 安装语义**：安装时 `Current` 为空，所有组件走 Install action
- **进度跟踪**：安装 DAG 执行进度持久化，支持控制器重启后断点续装
- **控制器接线**：在 `executePhaseFlow` 中插入安装 DAG 入口，对称于 `executeUpgradeDAG`
- **两方案并行设计**：方案 A（仅组件层）和方案 B（全量替换 PhaseFlow）均给出完整设计

### 1.2 设计范围

| 范围 | 说明 |
| ------ | ------ |
| InstallComponent 富化 | 从 bundle 的 ComponentVersion.Spec.Inline 回填 handler |
| 安装 DAG 构建 | 复用 BuildUpgradeDAG，输入源为 install.components |
| VersionContext 安装语义 | HasCurrent 方法 + BuildVersionContextForInstall |
| 进度跟踪 | 复用 DeclarativeUpgradeStatus |
| 控制器接线 | executeInstallDAG + Feature Gate 分发 |
| 方案 A | 仅 Helm/YAML 组件进 DAG，基础设施保留 PhaseFlow |
| 方案 B | 全部安装阶段组件化进 DAG，含 inline handler 注册 |

### 1.3 问题分析

**现状**：安装与升级存在根本性架构不对称。

| 维度 | 升级（当前） | 安装（当前） |
| ------ | ------ | ------ |
| 执行模型 | 拓扑 DAG（`ExecuteDAG`，批次内并行） | 顺序 `PhaseFlow`（`DeployPhases` 切片，串行） |
| 组件来源 | `ReleaseImage.Spec.Upgrade.Components` | `BKECluster.Spec.ClusterConfig`（硬编码 Phase） |
| Inline handler 注册 | 6 个（`catalog.go:19-26`） | 0 个（安装 Phase 不在 ComponentFactory 中） |
| 进度跟踪 | `DeclarativeUpgradeStatus`（Completed[]，断点续升） | 无（Phase 通过 `NeedExecute` 重新检查） |
| ReleaseImage 使用 | 是（bundle → DAG） | 否（`Spec.Install.Components` 仅用于版本上下文/兼容性检查） |
| 隐式依赖 | 1 个（`pre-upgrade-resources` 前置） | 0 个（`DefaultDependencyResolver` 返回 nil） |

**影响**：

- 主文档第 9.1 节安装流程图引用 `BuildInstallDAG(releaseImage)`，但代码中无此函数
- `ReleaseImageInstallComponent`（`api/v1alpha1/releaseimage_types.go:51-54`）仅有 `Name`/`Version`，无 `Inline` 字段
- 控制器仅有 `executeUpgradeDAG`（`bkecluster_upgrade_dag.go:47`），无 `executeInstallDAG`
- 安装路径完全不消费 `ReleaseImage.Spec.Install.Components` 进行执行

### 1.4 术语表

| 术语 | 定义 |
| ------ | ------ |
| **InstallDAG** | 安装 DAG，从 `ReleaseImage.Spec.Install.Components` 构建 |
| **UpgradeDAG** | 升级 DAG，从 `ReleaseImage.Spec.Upgrade.Components` 构建（已有） |
| **PhaseFlow** | 顺序 Phase 执行器（`pkg/phaseframe/phases/phase_flow.go`），当前安装路径 |
| **DeployPhases** | 安装阶段切片（`list.go:32-44`），定义安装顺序 |
| **Bundle enrich** | 从 bundle 的 ComponentVersion.Spec.Inline 回填 handler 信息 |
| **DeclarativeUpgradeStatus** | 声明式进度跟踪结构（`bkecluster_status.go:86`），安装场景复用 |

## 2. 现状分析

### 2.1 安装与升级的架构不对称

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           安装与升级路径对比                                      │
└─────────────────────────────────────────────────────────────────────────────────┘

  安装路径 (当前):                              升级路径 (当前):
  ┌──────────────┐                              ┌──────────────┐
  │ BKECluster   │                              │ BKECluster   │
  │ Reconciler   │                              │ Reconciler   │
  └──────┬───────┘                              └──────┬───────┘
         │                                             │
         ▼                                             ▼
  ┌──────────────┐                              ┌──────────────┐
  │ PhaseFlow    │  ← 顺序执行                   │ executeUpgradeDAG │ ← 拓扑批次
  │ (硬编码)     │                              │ (DAG 调度)     │
  └──────┬───────┘                              └──────┬───────┘
         │                                             │
         ▼                                             ▼
  ┌──────────────┐                              ┌──────────────┐
  │ DeployPhases │  ← BKECluster.Spec           │ ReleaseImage │ ← ReleaseImage.Spec
  │ (11 个 Phase) │    .ClusterConfig            │ .Upgrade     │   .Upgrade.Components
  └──────────────┘                              └──────────────┘
         │                                             │
         │  ❌ 不使用 ReleaseImage                     │  ✅ 使用 ReleaseImage
         │  ❌ 不使用 install.components               │  ✅ 使用 upgrade.components
         │  ❌ 无进度跟踪                               │  ✅ DeclarativeUpgradeStatus
         │  ❌ 无并行                                   │  ✅ 批次内并行
         ▼                                             ▼
  ┌──────────────┐                              ┌──────────────┐
  │ PostDeploy   │                              │ 完成         │
  │ Phases       │                              │ ClusterReady │
  └──────────────┘                              └──────────────┘
```

### 2.2 现有安装路径（PhaseFlow）

安装入口在 `controllers/capbke/bkecluster_controller.go:214-254` 的 `executePhaseFlow`：

```go
func (r *BKEClusterReconciler) executePhaseFlow(...) (ctrl.Result, error) {
    phaseCtx := phaseframe.NewReconcilePhaseCtx(ctx)...
    if r.shouldUseDeclarativeUpgrade(bkeCluster) {   // 仅升级
        ...executeUpgradeDAG...
    }
    flow := phases.NewPhaseFlow(phaseCtx)            // 安装路径
    flow.CalculatePhase(oldBkeCluster, bkeCluster)
    flow.Execute()
}
```

`PhaseFlow` 的 Phase 集合由 `FullPhasesRegisFunc`（`phase_flow.go:41-45`）在 `init()` 时硬编码：

```txt
FullPhasesRegisFunc = CommonPhases + DeployPhases + PostDeployPhases

CommonPhases:     Finalizer, Paused, ClusterManage, DeleteOrReset, DryRun
DeployPhases:     BKEAgent, NodesEnv, ClusterAPIObj, Certs, LoadBalance,
                  MasterInit, MasterJoin, WorkerJoin, AddonDeploy,
                  NodesPostProcess, AgentSwitch
PostDeployPhases: ProviderSelfUpgrade, AgentUpgrade, ContainerdUpgrade,
                  EtcdUpgrade, WorkerUpgrade, MasterUpgrade,
                  WorkerDelete, MasterDelete, ComponentUpgrade,
                  ClusterAPIManagerManifest, Cluster
```

Phase 通过 `executePhases`（`phase_flow.go:206`）**严格按切片顺序串行执行**，每个 Phase 的 `NeedExecute` 决定跳过/运行。无拓扑排序、无并行。

### 2.3 现有升级路径（DAG）

升级入口在 `controllers/capbke/bkecluster_upgrade_dag.go:47-129` 的 `executeUpgradeDAG`：

```txt
executeUpgradeDAG 流程:
  1. 解析 ReleaseImage bundle (resolveUpgradeBundle)
  2. 构建 VersionContext (BuildVersionContextForUpgrade)
  3. 构建升级 DAG (upgrade.BuildDAGFromBundle)
  4. 初始化进度跟踪 (ensureDeclarativeUpgradeProgress)
  5. 构建 ComponentFactory (NewFactoryFromBundle)
  6. 构建 Scheduler (dagexec.NewScheduler)
  7. 执行 DAG (sched.ExecuteDAG)
  8. 完成升级 (completeDeclarativeUpgrade)
```

`BuildDAGFromBundle`（`pkg/upgrade/bundle.go:24-33`）读取 `bundle.Release.Spec.Upgrade.Components`，通过 `enrichUpgradeComponent` 从 bundle 的 `ComponentVersion.Spec.Inline` 回填 handler，然后调用 `topology.BuildUpgradeDAG` 构建拓扑 DAG。

### 2.4 ReleaseImage install.components 使用现状

`Spec.Install.Components` 当前**仅用于版本上下文和兼容性检查**，不用于执行：

| 使用位置 | 文件:行 | 用途 |
| --------- | ------ | ------ |
| `applyReleaseComponents` | `pkg/upgrade/build_release.go:65-81` | 填充 VersionContext 的 Target/Current（升级场景） |
| `releaseComponents` | `pkg/release/compatibility/flatten.go:72-84` | 展平子组件进行兼容性检查 |

`ReleaseImageInstallComponent`（`api/v1alpha1/releaseimage_types.go:51-54`）仅有 `Name`/`Version`，无 `Inline` 字段——与 `ReleaseImageUpgradeComponent`（有 `Inline *ReleaseImageUpgradeInline`）不对称。

安装路径（`DeployPhases`）完全不读取 `ReleaseImage`——它从 `BKECluster.Spec.ClusterConfig.Cluster`（如 `KubernetesVersion`/`EtcdVersion`）和 `Spec.ClusterConfig.Addons` 获取组件版本和配置。

## 3. 共享设计（两方案通用）

以下设计为方案 A 和方案 B 共享的基础设施，两方案差异部分见第 4 章和第 5 章。

### 3.1 InstallComponent 富化机制（Bundle enrich）

**设计思路**：对称于 `enrichUpgradeComponent`（`pkg/upgrade/bundle.go:93-109`），不修改 `ReleaseImageInstallComponent` API 类型，从 bundle 的 `ComponentVersion.Spec.Inline` 回填 handler 信息。

**关键点**：`ReleaseImageInstallComponent`（`api/v1alpha1/releaseimage_types.go:51-54`）仅有 `Name`/`Version`，无 `Inline`。富化时将其转换为 `ReleaseImageUpgradeComponent`（复用已有类型作为 DAG 输入），这样 `BuildUpgradeDAG`（`pkg/topology/build.go:25`）可直接复用，无需新增 `BuildInstallDAG` 函数。

```go
// pkg/upgrade/bundle.go 新增

// InstallComponentsFromBundle 返回从 bundle 富化后的安装组件列表。
// 读取 bundle.Release.Spec.Install.Components，通过 bundle.Components 查找
// ComponentVersion.Spec.Inline 回填 handler 信息。
// 返回 []ReleaseImageUpgradeComponent 以复用 BuildUpgradeDAG。
func InstallComponentsFromBundle(bundle *releasemanifest.Bundle) ([]cvv1alpha1.ReleaseImageUpgradeComponent, error) {
    if bundle == nil {
        return nil, fmt.Errorf("release bundle is nil")
    }
    if bundle.Release.Spec.Install == nil || len(bundle.Release.Spec.Install.Components) == 0 {
        return nil, fmt.Errorf("release bundle has no install components")
    }
    out := make([]cvv1alpha1.ReleaseImageUpgradeComponent, 0, len(bundle.Release.Spec.Install.Components))
    for _, comp := range bundle.Release.Spec.Install.Components {
        out = append(out, enrichInstallComponent(comp, bundle))
    }
    return out, nil
}

// enrichInstallComponent 从 bundle 的 ComponentVersion.Spec.Inline 回填 handler。
// 对称于 enrichUpgradeComponent (bundle.go:93-109)。
//
// 与升级侧的差异:
// - 升级侧 enrichUpgradeComponent 有短路逻辑: if comp.Inline != nil { return comp }
//   (用户在 ReleaseImage 层已声明 Inline 时跳略富化)
// - 安装侧无需此短路，因为 ReleaseImageInstallComponent 无 Inline 字段，
//   必须从 bundle 富化。
func enrichInstallComponent(
    comp cvv1alpha1.ReleaseImageInstallComponent,
    bundle *releasemanifest.Bundle,
) cvv1alpha1.ReleaseImageUpgradeComponent {
    enriched := cvv1alpha1.ReleaseImageUpgradeComponent{
        Name:    comp.Name,
        Version: comp.Version,
    }
    cv, ok := bundle.Components[releasemanifest.ComponentKey(comp.Name, comp.Version)]
    if !ok || cv.Spec.Inline == nil {
        return enriched
    }
    enriched.Inline = &cvv1alpha1.ReleaseImageUpgradeInline{
        Handler: cv.Spec.Inline.Handler,
        Version: cv.Spec.Inline.Version,
    }
    return enriched
}
```

**与升级侧的差异**：升级侧的 `enrichUpgradeComponent` 有一个短路逻辑——`if comp.Inline != nil { return comp }`（用户在 ReleaseImage 层已声明 Inline 时跳略富化）。安装侧无需此短路，因为 `ReleaseImageInstallComponent` 无 `Inline` 字段，必须从 bundle 富化。

### 3.2 安装 DAG 构建（复用 BuildUpgradeDAG）

**设计思路**：不新增 `BuildInstallDAG` 函数。`InstallComponentsFromBundle` 返回 `[]ReleaseImageUpgradeComponent` 后，直接复用 `BuildUpgradeDAG`（`pkg/topology/build.go:25`）。安装 DAG 和升级 DAG 的**唯一差异是输入数据源**（`Spec.Install.Components` vs `Spec.Upgrade.Components`），DAG 构建逻辑（拓扑排序、依赖解析、批次划分）完全相同。

```go
// pkg/upgrade/bundle.go 新增

// BuildInstallDAGFromBundle 从 release bundle 构建安装 DAG。
// 对称于 BuildDAGFromBundle (bundle.go:24-33)，但读取 Spec.Install.Components。
func BuildInstallDAGFromBundle(bundle *releasemanifest.Bundle, resolve topology.DependencyResolver) (*topology.UpgradeDAG, error) {
    components, err := InstallComponentsFromBundle(bundle)
    if err != nil {
        return nil, err
    }
    return topology.BuildUpgradeDAG(
        components,
        topology.MergeDependencyResolver(resolve, topology.DefaultDependencyResolver()),
    )
}
```

**依赖解析器复用**：`BundleDependencyResolver`（`bundle.go:51-63`）从 `ComponentVersion.Spec.Dependencies` 读取依赖，与 install/upgrade 无关——它只关心 bundle 中的 CV 对象。因此安装 DAG 可直接复用 `BundleDependencyResolver(bundle)`。

**安装侧隐式依赖**：升级侧有 `appendImplicitPreUpgradeDependency`（`bundle.go:65-79`）将 `pre-upgrade-resources` 注入为所有组件的隐式前置。安装侧目前无对称机制。如果 `install.components` 中包含类似的"安装前资源准备"组件，可新增 `appendImplicitPreInstallDependency`，但**最小集不引入**——安装组件的依赖关系完全由 `ComponentVersion.Spec.Dependencies` 显式声明。

### 3.3 VersionContext 扩展（HasCurrent + BuildVersionContextForInstall）

#### 3.3.1 HasCurrent 方法（KEP 已设计，代码未实现）

**现状**：`pkg/upgrade/context.go` 中 `VersionContext` 仅有 `HasTarget`（`context.go:76`）和 `GetCurrent`（`context.go:56`），**无 `HasCurrent`**。KEP 主文档（`声明式集群版本升级方案-支持 Helm 组件.md:4358-4388`）已设计此方法但代码未实现。安装 DAG 需要 `HasCurrent` 让 `HelmComponentExecutor`（主文档 4563 行）正确判定 Install vs Upgrade。

```go
// pkg/upgrade/context.go 新增

// HasCurrent 报告组件是否已有已安装版本记录。
// 安装时所有组件 HasCurrent=false → Executor 走 Install action。
// 升级时 HasCurrent=true → Executor 走 Upgrade action。
func (vc *VersionContext) HasCurrent(name string) bool {
    if vc == nil {
        return false
    }
    vc.mu.RLock()
    defer vc.mu.RUnlock()
    _, ok := vc.Current[name]
    return ok
}

// CurrentVersion 返回组件当前版本及是否存在。
func (vc *VersionContext) CurrentVersion(name string) (string, bool) {
    if vc == nil {
        return "", false
    }
    vc.mu.RLock()
    defer vc.mu.RUnlock()
    v, ok := vc.Current[name]
    return v, ok
}
```

#### 3.3.2 BuildVersionContextForInstall

**设计思路**：安装时 `VersionContext.Current` 为空（全新安装无已安装版本），`Target` 从 `bundle.Release.Spec.Install.Components` 填充。`HasCurrent` 返回 false 触发所有组件走 Install action。

```go
// pkg/upgrade/build_release.go 新增

// BuildVersionContextForInstall 构建安装场景的 VersionContext。
// Target 从 bundle 的 install.components 填充（install + upgrade 合并，upgrade 覆盖）。
// Current 为空（全新安装无已安装版本），使所有组件 HasCurrent=false → Install action。
//
// 与 BuildVersionContextForUpgrade 的差异 (build_release.go:40-63):
// - 升级侧: Target 从 targetBundle 填充, Current 从 currentBundle 或 BKECluster.Status 填充
// - 安装侧: Target 从 bundle 填充, Current 不填充 (空 map)
// - applyReleaseComponents (build_release.go:65-81) 已同时遍历 install + upgrade components, 直接复用
func BuildVersionContextForInstall(
    bundle *releasemanifest.Bundle,
    bc *bkev1beta1.BKECluster,
) *VersionContext {
    vc := NewVersionContext()
    if bundle != nil {
        // 复用 applyReleaseComponents：同时遍历 install + upgrade components
        // upgrade 覆盖 install 同名项（与升级侧一致）
        FillTargetFromBundle(vc, bundle)
    } else if bc != nil {
        // fallback: 从 BKECluster.Spec 推断目标版本
        legacy := BuildVersionContextFromBKECluster(bc)
        for name, version := range legacy.Target {
            if version != "" {
                vc.SetTarget(name, version)
            }
        }
    }
    // Current 保持空 map → 所有组件 HasCurrent=false
    return vc
}
```

#### 3.3.3 NeedsUpgrade 在安装场景的行为

`NeedsUpgrade`（`context.go:81-86`）定义为 `HasTarget(name) && GetCurrent(name) != GetTarget(name)`。安装时 `GetCurrent(name)` 返回空字符串，`GetTarget(name)` 返回目标版本，因此 `NeedsUpgrade` 返回 **true**——组件需要执行。

KEP 主文档中 `HelmComponentExecutor`（主文档 4541 行）用 `!vc.NeedsUpgrade(component.Name)` 判断跳过：

```go
if vc != nil && !vc.NeedsUpgrade(component.Name) {
    execCtx.Log.Info("component %s already at target version, skipping", component.Name)
    return nil
}
```

安装时 `NeedsUpgrade=true`，不跳过——正确行为。同时 `HasCurrent=false` 触发 Install action（主文档 4563 行的 `default` 分支）——也正确。

**结论**：`VersionContext` 现有 `NeedsUpgrade` + 新增 `HasCurrent` 已能正确支持安装场景，无需修改 `NeedsUpgrade` 语义。

### 3.4 进度跟踪（复用 DeclarativeUpgradeStatus）

**设计思路**：复用现有 `DeclarativeUpgradeStatus`（`api/bkecommon/v1beta1/bkecluster_status.go:86-112`）结构。安装时 `TargetVersion` = 安装版本，`Completed[]` 记录已完成组件，控制器重启后通过 `IsCompleted`（`bkecluster_status.go:190`）跳过已完成组件，实现断点续装。

**与升级侧的差异**：

| 维度 | 升级 | 安装 |
| ------ | ------ | ------ |
| `TargetVersion` | 升级目标版本（hop target） | 安装目标版本（desiredVersion） |
| `Current` 在 VersionContext | 从 currentBundle/BKECluster.Status 填充 | 空 map |
| `EnsureInitialized` 调用 | 升级前调用（`bkecluster_upgrade_dag.go:157`） | 安装前调用（对称） |
| `MarkCompleted`/`IsCompleted` | 组件完成后记录 | 完全复用 |
| 完成清理 | `completeDeclarativeUpgrade` 清除 upgrade-ready 注解 | 安装完成后设置 ClusterStatus=Ready |

**语义歧义处理**：`DeclarativeUpgradeStatus` 字段名含 "Upgrade"，但结构完全适用于安装。**最小集不改名**（避免 API 变更 + deepcopy 重生成），通过注释说明安装场景复用。后续可考虑重命名为 `DeclarativeProgressStatus`（带 alias 向后兼容）。

**Scheduler 跳过逻辑复用**：`shouldSkipComponent`（`scheduler.go:276-285`）通过 `DeclarativeUpgradeStatus.IsCompleted` 跳过已完成组件，安装场景完全适用——控制器重启后，已完成的安装组件会被跳过，未完成的继续执行。

### 3.5 Feature Gate 集成

**设计思路**：安装 DAG 与升级 DAG 共用同一 Feature Gate（`HelmComponentAnnotationKey`，主文档 5221 行）。Feature Gate ON 时，新集群安装走 install DAG 路径（Helm/YAML 组件通过 Executor 执行）；Feature Gate OFF 时，回退到 PhaseFlow 顺序执行。

```go
// controllers/capbke/bkecluster_controller.go executePhaseFlow 扩展

func (r *BKEClusterReconciler) executePhaseFlow(...) (ctrl.Result, error) {
    phaseCtx := phaseframe.NewReconcilePhaseCtx(ctx)...
    
    // 升级路径 (已有)
    if r.shouldUseDeclarativeUpgrade(bkeCluster) {
        ...executeUpgradeDAG...
    }
    
    // 安装路径 (新增): 首次部署 + Feature Gate ON
    if r.isFreshInstall(bkeCluster) && featuregate.HelmComponentEnabled(bkeCluster) {
        return r.executeInstallDAG(ctx, phaseCtx, oldBkeCluster, bkeCluster, bkeLogger)
    }
    
    // 旧路径: PhaseFlow 顺序执行
    flow := phases.NewPhaseFlow(phaseCtx)
    ...
}

// isFreshInstall 判断是否首次安装
// 复用现有 getNodeFlags 的 deployFlag 逻辑 (bkecluster_controller.go:620: deployFlag = nodeCount == 0)
func (r *BKEClusterReconciler) isFreshInstall(bkeCluster *bkev1beta1.BKECluster) bool {
    // 集群无已就绪节点 + 无 DeclarativeUpgrade 完成记录
    return phaseutil.GetReadyNodesWithBKENodes(bkeCluster).Length() == 0
}
```

## 4. 方案 A：仅组件层

### 4.1 架构定位

安装流程保持三段式结构，DAG 仅替换中间的**声明式组件安装**部分：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        方案 A：安装流程三段式结构                                  │
└─────────────────────────────────────────────────────────────────────────────────┘

  ┌──────────────────────────────────────────────┐
  │  段 1: CommonPhases (顺序, 控制器前置门控)     │  ← 保留 PhaseFlow 不变
  │  ├── EnsureFinalizer                         │
  │  ├── EnsurePaused                            │
  │  ├── EnsureClusterManage                     │
  │  ├── EnsureDeleteOrReset                     │
  │  └── EnsureDryRun                            │
  └──────────────────────┬───────────────────────┘
                         │
                         ▼
  ┌──────────────────────────────────────────────┐
  │  段 2: DeployPhases (顺序, 基础设施部署)       │  ← 保留 PhaseFlow 不变
  │  ├── EnsureBKEAgent → EnsureNodesEnv         │
  │  ├── EnsureClusterAPIObj → EnsureCerts       │
  │  ├── EnsureLoadBalance                       │
  │  ├── EnsureMasterInit → EnsureMasterJoin     │
  │  ├── EnsureWorkerJoin                        │
  │  └── (Master/Worker 节点就绪)                │
  └──────────────────────┬───────────────────────┘
                         │
                         ▼
  ┌──────────────────────────────────────────────┐
  │  段 3: InstallDAG (拓扑批次, 声明式组件)       │  ← 新增, 替代 EnsureAddonDeploy
  │  ├── coredns (helm)          ← HelmComponentExecutor
  │  ├── openfuyao-addon (yaml)  ← YamlComponentExecutor
  │  ├── kube-proxy (yaml)       ← YamlComponentExecutor
  │  └── ... (install.components 中的 Helm/YAML 组件)
  └──────────────────────┬───────────────────────┘
                         │
                         ▼
  ┌──────────────────────────────────────────────┐
  │  段 4: PostDeployPhases (顺序, 后置处理)       │  ← 保留 PhaseFlow 不变
  │  ├── EnsureNodesPostProcess                  │
  │  ├── EnsureAgentSwitch                       │
  │  └── EnsureCluster (健康检查)                │
  └──────────────────────────────────────────────┘
```

**关键设计点**：

- DAG 仅处理 `install.components` 中的 **Helm/YAML 组件**（coredns、openfuyao-addon 等）
- 基础设施阶段（BKEAgent、Certs、MasterInit、WorkerJoin 等）保留 PhaseFlow 顺序执行
- CommonPhases 保留为控制器前置门控
- EnsureAddonDeploy 被 InstallDAG 替代（addon 部署逻辑迁移到 DAG）

### 4.2 控制器流程

```go
// controllers/capbke/bkecluster_install_dag.go 新增

// executeInstallDAG 执行声明式安装组件的 DAG。
// 对称于 executeUpgradeDAG (bkecluster_upgrade_dag.go:47-129)。
// 在 DeployPhases 完成后、PostDeployPhases 之前调用。
func (r *BKEClusterReconciler) executeInstallDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    bkeLogger *bkev1beta1.BKELogger,
) (ctrl.Result, error) {
    // 1. 解析 ReleaseImage bundle
    //    安装时目标版本 = BKECluster.Spec.DesiredVersion
    //    (或 ClusterConfig.Cluster.OpenFuyaoVersion)
    installVersion := clusterversion.OpenFuyaoVersionForBKECluster(newCluster)
    bundle, releaseImage, err := r.resolveInstallBundle(ctx, newCluster, installVersion)
    if err != nil {
        if isReleaseImageNotReady(err) {
            bkeLogger.Info("waiting for release image", "reason", err.Error())
            return ctrl.Result{RequeueAfter: releaseImageRequeueInterval}, nil
        }
        return ctrl.Result{}, err
    }

    // 2. 构建 VersionContext (安装场景: Current 为空)
    phaseCtx.VersionContext = upgrade.BuildVersionContextForInstall(bundle, newCluster)

    // 3. 构建安装 DAG
    dag, err := upgrade.BuildInstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    if err != nil {
        return ctrl.Result{}, errors.Wrap(err, "build install DAG")
    }

    bkeLogger.Info("declarative install",
        "installVersion", installVersion,
        "releaseImage", releaseImage.Name,
        "phase", releaseImage.Status.Phase,
        "components", len(dag.NodeNames()),
        "source", bundle.Source,
    )

    // 4. 初始化进度跟踪 (复用 DeclarativeUpgradeStatus)
    if err := r.ensureDeclarativeInstallProgress(newCluster, installVersion); err != nil {
        return ctrl.Result{}, err
    }

    // 5. 构建 Scheduler (Feature Gate ON → 注册 Helm/YAML Executor)
    factory, err := componentfactory.NewFactoryFromBundle(bundle)
    if err != nil {
        return ctrl.Result{}, errors.Wrap(err, "build component factory from release bundle")
    }
    sched := dagexec.NewScheduler(dagexec.Config{
        InlineRunner:        &componentfactory.PhaseRunner{Factory: factory},
        ManifestStore:       manifest.NewBundleStore(bundle),
        ManifestApplier:     r.buildManifestApplier(ctx, phaseCtx, newCluster, bkeLogger),
        MaxParallelPerBatch: 0,
    })

    // 6. 执行 DAG
    if err := sched.ExecuteDAG(ctx, phaseCtx, oldCluster, newCluster, dag); err != nil {
        _ = r.patchClusterStatus(newCluster, bkev1beta1.ClusterDeployFailed)
        if res, requeue := dagexec.RequeueAwareError(err); requeue {
            return res, err
        }
        return ctrl.Result{}, err
    }

    // 7. 安装完成
    return ctrl.Result{}, nil
}

// resolveInstallBundle 对称于 resolveUpgradeBundle (bkecluster_upgrade_dag.go:182-211)
func (r *BKEClusterReconciler) resolveInstallBundle(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    installVersion string,
) (*releasemanifest.Bundle, *cvv1alpha1.ReleaseImage, error) {
    ri, err := clusterversion.ResolveReleaseImageForVersion(ctx, r.Client, bkeCluster.Namespace, installVersion)
    if err != nil {
        return nil, nil, err
    }
    switch ri.Status.Phase {
    case cvv1alpha1.ReleaseImagePhaseValid:
        // continue
    case cvv1alpha1.ReleaseImagePhaseInvalid,
        cvv1alpha1.ReleaseImagePhaseManifestMissing,
        cvv1alpha1.ReleaseImagePhaseCompatibilityFailed:
        return nil, ri, fmt.Errorf("release image %s/%s phase %s: %s",
            ri.Namespace, ri.Name, ri.Status.Phase, ri.Status.Message)
    default:
        return nil, ri, &releaseImagePendingError{
            msg: fmt.Sprintf("release image %s/%s phase %s", ri.Namespace, ri.Name, ri.Status.Phase),
        }
    }
    bundle, err := r.releaseStore().ResolveRelease(ctx, releaseRefFromCR(ri))
    if err != nil {
        return nil, ri, errors.Wrapf(err, "resolve release %s/%s", ri.Namespace, ri.Name)
    }
    return bundle, ri, nil
}

// ensureDeclarativeInstallProgress 对称于 ensureDeclarativeUpgradeProgress
// (bkecluster_upgrade_dag.go:147-159), 复用 DeclarativeUpgradeStatus 结构
func (r *BKEClusterReconciler) ensureDeclarativeInstallProgress(
    bkeCluster *bkev1beta1.BKECluster,
    installVersion string,
) error {
    if bkeCluster == nil {
        return nil
    }
    now := metav1.Now()
    return mergecluster.SyncStatusUntilComplete(r.Client, bkeCluster, func(bc *bkev1beta1.BKECluster) {
        if bc.Status.DeclarativeUpgrade == nil {
            bc.Status.DeclarativeUpgrade = &confv1beta1.DeclarativeUpgradeStatus{}
        }
        // EnsureInitialized resets completion when target changes and clears FinishedAt.
        bc.Status.DeclarativeUpgrade.EnsureInitialized(installVersion, now)
    })
}
```

### 4.3 install.components 内容约束

方案 A 下，`install.components` **仅包含 Helm/YAML 组件**（coredns、openfuyao-addon、kube-proxy 等），**不包含**基础设施组件（etcd、kubernetes-master、kubernetes-worker、containerd、bkeagent）——这些仍由 DeployPhases 顺序执行。

`install.components` 示例：

```yaml
spec:
  install:
    components:
      - name: coredns           # type: helm (HelmComponentExecutor)
        version: v1.11.1
      - name: openfuyao-addon   # type: yaml (YamlComponentExecutor)
        version: v26.03
      - name: kube-proxy        # type: yaml
        version: v1.29.0
```

**类型过滤**：`InstallComponentsFromBundle` 在方案 A 中**仅保留 `type=helm`/`type=yaml` 的组件**，inline 类型由 PhaseFlow 处理：

```go
// 方案 A: InstallComponentsFromBundle 带类型过滤

func InstallComponentsFromBundle(bundle *releasemanifest.Bundle) ([]cvv1alpha1.ReleaseImageUpgradeComponent, error) {
    if bundle == nil {
        return nil, fmt.Errorf("release bundle is nil")
    }
    if bundle.Release.Spec.Install == nil || len(bundle.Release.Spec.Install.Components) == 0 {
        return nil, fmt.Errorf("release bundle has no install components")
    }
    out := make([]cvv1alpha1.ReleaseImageUpgradeComponent, 0, len(bundle.Release.Spec.Install.Components))
    for _, comp := range bundle.Release.Spec.Install.Components {
        cv, ok := bundle.Components[releasemanifest.ComponentKey(comp.Name, comp.Version)]
        if !ok {
            continue // 跳过无 CV 的组件
        }
        // 方案 A: 仅保留 helm/yaml 类型, inline 类型由 PhaseFlow 处理
        if cv.Spec.Type == cvv1alpha1.ComponentTypeHelm || cv.Spec.Type == cvv1alpha1.ComponentTypeYAML {
            out = append(out, enrichInstallComponent(comp, bundle))
        }
    }
    return out, nil
}
```

**注意**：`InstallComponentsFromBundle` 的类型过滤行为在方案 A 和方案 B 之间不同。可通过参数控制，或拆分为两个函数（`InstallHelmYamlComponentsFromBundle` vs `InstallAllComponentsFromBundle`）。推荐通过参数控制避免函数 proliferate：

```go
// 通用版本, 通过 filter 参数控制
func InstallComponentsFromBundle(bundle *releasemanifest.Bundle, filter func(cvv1alpha1.ComponentType) bool) ([]cvv1alpha1.ReleaseImageUpgradeComponent, error) {
    // ...
    for _, comp := range bundle.Release.Spec.Install.Components {
        cv, ok := bundle.Components[releasemanifest.ComponentKey(comp.Name, comp.Version)]
        if !ok {
            continue
        }
        if filter != nil && !filter(cv.Spec.Type) {
            continue
        }
        out = append(out, enrichInstallComponent(comp, bundle))
    }
    return out, nil
}

// 方案 A 调用:
// InstallComponentsFromBundle(bundle, func(t cvv1alpha1.ComponentType) bool {
//     return t == cvv1alpha1.ComponentTypeHelm || t == cvv1alpha1.ComponentTypeYAML
// })

// 方案 B 调用:
// InstallComponentsFromBundle(bundle, nil) // 不过滤
```

### 4.4 与 PhaseFlow 的衔接

**关键问题**：`executePhaseFlow`（`bkecluster_controller.go:214-254`）当前是全量顺序执行。方案 A 需要在 DeployPhases 和 PostDeployPhases 之间插入 install DAG。

**实现方式**：不修改 `PhaseFlow` 内部逻辑，而是在 `executePhaseFlow` 中**分多段调用**：

```go
func (r *BKEClusterReconciler) executePhaseFlow(...) (ctrl.Result, error) {
    phaseCtx := ...
    
    if r.shouldUseDeclarativeUpgrade(bkeCluster) {
        ...executeUpgradeDAG...
    }
    
    // 方案 A: Feature Gate ON 时，分段执行
    if r.isFreshInstall(bkeCluster) && featuregate.HelmComponentEnabled(bkeCluster) {
        // 段 1+2: CommonPhases + DeployPhases (不含 EnsureAddonDeploy)
        deployFlow := phases.NewPhaseFlow(phaseCtx,
            phases.WithPhaseSet(append(phases.CommonPhases, phases.DeployPhasesExceptAddon...)))
        if res, err := deployFlow.CalculateAndExecute(); err != nil {
            return res, err
        }
        
        // 段 3: Install DAG (Helm/YAML 组件)
        if res, err := r.executeInstallDAG(ctx, phaseCtx, oldBkeCluster, bkeCluster, bkeLogger); err != nil {
            return res, err
        }
        
        // 段 4: PostDeployPhases
        postFlow := phases.NewPhaseFlow(phaseCtx, phases.WithPhaseSet(phases.PostDeployPhases))
        return postFlow.CalculateAndExecute()
    }
    
    // 旧路径: 全量 PhaseFlow
    flow := phases.NewPhaseFlow(phaseCtx)
    ...
}
```

### 4.5 PhaseFlow 分段执行扩展

当前 `PhaseFlow` 的 phase 集合由 `FullPhasesRegisFunc`（`phase_flow.go:41-45`）在 `init()` 时硬编码为全量集合。需支持按子集执行。

**设计：WithPhaseSet option 模式**

```go
// pkg/phaseframe/phases/phase_flow.go 扩展

// PhaseFlowOption PhaseFlow 配置选项
type PhaseFlowOption func(*PhaseFlow)

// WithPhaseSet 指定要执行的 Phase 集合 (替代默认的 FullPhasesRegisFunc)
func WithPhaseSet(phases []func(ctx *phaseframe.PhaseContext) phaseframe.Phase) PhaseFlowOption {
    return func(f *PhaseFlow) {
        f.phases = phases
    }
}

// NewPhaseFlow 创建 PhaseFlow, 支持可选配置
func NewPhaseFlow(ctx *phaseframe.PhaseContext, opts ...PhaseFlowOption) *PhaseFlow {
    flow := &PhaseFlow{
        ctx:    ctx,
        phases: FullPhasesRegisFunc, // 默认全量
    }
    for _, opt := range opts {
        opt(flow)
    }
    return flow
}

// CalculateAndExecute 便捷方法: CalculatePhase + Execute
func (f *PhaseFlow) CalculateAndExecute() (ctrl.Result, error) {
    if err := f.CalculatePhase(f.ctx.OldBKECluster, f.ctx.BKECluster); err != nil {
        return ctrl.Result{}, err
    }
    return f.Execute()
}
```

**新增 DeployPhasesExceptAddon**：

```go
// pkg/phaseframe/phases/list.go 新增

// DeployPhasesExceptAddon DeployPhases 去除 EnsureAddonDeploy
// 方案 A: EnsureAddonDeploy 的 addon 部署逻辑由 InstallDAG 替代
var DeployPhasesExceptAddon = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureBKEAgent,
    NewEnsureNodesEnv,
    NewEnsureClusterAPIObj,
    NewEnsureCerts,
    NewEnsureLoadBalance,
    NewEnsureMasterInit,
    NewEnsureMasterJoin,
    NewEnsureWorkerJoin,
    // NewEnsureAddonDeploy,  ← 移除, 由 InstallDAG 替代
    NewEnsureNodesPostProcess,
    NewEnsureAgentSwitch,
}
```

## 5. 方案 B：全量替换 PhaseFlow

### 5.1 架构定位

所有安装阶段（基础设施 + 声明式组件）统一进入 DAG：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                   方案 B：全量替换 PhaseFlow 安装流程                              │
└─────────────────────────────────────────────────────────────────────────────────┘

  ┌──────────────────────────────────────────────┐
  │  段 1: CommonPhases (顺序, 控制器前置门控)     │  ← 不进 DAG
  │  ├── EnsureFinalizer                         │
  │  ├── EnsurePaused / EnsureClusterManage      │
  │  ├── EnsureDeleteOrReset                     │
  │  └── EnsureDryRun                            │
  └──────────────────────┬───────────────────────┘
                         │
                         ▼
  ┌──────────────────────────────────────────────┐
  │  段 2: InstallDAG (拓扑批次, 全量组件)         │  ← 替换 DeployPhases
  │  │                                           │
  │  │ Batch 1 (基础设施前置):                    │
  │  │  ├── bkeagent (inline)                    │
  │  │  └── nodes-env (inline)                   │
  │  │                                           │
  │  │ Batch 2 (集群初始化):                      │
  │  │  ├── cluster-api-obj (inline)             │
  │  │  └── certs (inline)                       │
  │  │                                           │
  │  │ Batch 3 (控制面):                          │
  │  │  ├── load-balance (inline)                │
  │  │  └── kubernetes-master (inline)           │
  │  │                                           │
  │  │ Batch 4 (工作节点):                        │
  │  │  ├── kubernetes-master-join (inline)      │
  │  │  └── kubernetes-worker (inline)           │
  │  │                                           │
  │  │ Batch 5 (声明式组件):                      │
  │  │  ├── coredns (helm)                       │
  │  │  ├── openfuyao-addon (yaml)               │
  │  │  └── kube-proxy (yaml)                    │
  └──────────────────────┬───────────────────────┘
                         │
                         ▼
  ┌──────────────────────────────────────────────┐
  │  段 3: PostDeployPhases (顺序, 后置处理)       │  ← 保留 PhaseFlow
  │  ├── EnsureNodesPostProcess                  │
  │  ├── EnsureAgentSwitch                       │
  │  └── EnsureCluster (健康检查)                │
  └──────────────────────────────────────────────┘
```

**关键设计点**：

- CommonPhases 保留为控制器前置门控（不是组件，是控制器逻辑）
- DeployPhases 全部组件化进 DAG，每个 Phase 成为 DAG 的一个 inline 节点
- PostDeployPhases 保留 PhaseFlow（后置处理 + 健康检查）
- 需为每个安装 Phase 注册 inline handler + 创建 ComponentVersion CR

### 5.2 安装 Inline Handler 注册

**现状**：`componentfactory/registry.go:24-40` 仅注册 6 个**升级** handler：

| Handler 常量 | 值 | 对应 Phase |
| --- | --- | --- |
| `InlineHandlerEtcdUpgrade` | `"EnsureEtcdUpgrade"` | `NewEnsureEtcdUpgrade` |
| `InlineHandlerMasterUpgrade` | `"EnsureMasterUpgrade"` | `NewEnsureMasterUpgrade` |
| `InlineHandlerWorkerUpgrade` | `"EnsureWorkerUpgrade"` | `NewEnsureWorkerUpgrade` |
| `InlineHandlerContainerdUpgrade` | `"EnsureContainerdUpgrade"` | `NewEnsureContainerdUpgrade` |
| `InlineHandlerAgentUpgrade` | `"EnsureAgentUpgrade"` | `NewEnsureAgentUpgrade` |
| `InlineHandlerPreUpgradeResources` | `"EnsurePreUpgradeResources"` | `NewEnsurePreUpgradeResourcesWithComponentVersion` (特殊) |

安装 Phase（`EnsureMasterInit`、`EnsureWorkerJoin`、`EnsureBKEAgent` 等）**未注册**，`ComponentFactory.Resolve()` 会返回 `"unknown inline handler"` 错误（`registry.go:37`）。

**新增安装 handler 常量**：

```go
// pkg/upgrade/catalog.go 新增

// 安装 inline handler 名称 (匹配 phaseframe.Phase.Name() 和
// ComponentVersion.spec.inline.handler)
const (
    InlineHandlerBKEAgentInstall    = "EnsureBKEAgent"
    InlineHandlerNodesEnvInstall    = "EnsureNodesEnv"
    InlineHandlerClusterAPIInstall  = "EnsureClusterAPIObj"
    InlineHandlerCertsInstall       = "EnsureCerts"
    InlineHandlerLoadBalanceInstall = "EnsureLoadBalance"
    InlineHandlerMasterInitInstall  = "EnsureMasterInit"
    InlineHandlerMasterJoinInstall  = "EnsureMasterJoin"
    InlineHandlerWorkerJoinInstall  = "EnsureWorkerJoin"
    InlineHandlerAddonDeployInstall = "EnsureAddonDeploy"
    InlineHandlerNodesPostProcess   = "EnsureNodesPostProcess"
    InlineHandlerAgentSwitch        = "EnsureAgentSwitch"
)
```

**注册扩展**：

```go
// pkg/componentfactory/registry.go registerInlineHandler 扩展

func registerInlineHandler(f *ComponentFactory, handler, version string) error {
    switch handler {
    // === 升级 handlers (已有) ===
    case upgrade.InlineHandlerEtcdUpgrade:
        f.Register(handler, version, phases.NewEnsureEtcdUpgrade)
    case upgrade.InlineHandlerMasterUpgrade:
        f.Register(handler, version, phases.NewEnsureMasterUpgrade)
    case upgrade.InlineHandlerWorkerUpgrade:
        f.Register(handler, version, phases.NewEnsureWorkerUpgrade)
    case upgrade.InlineHandlerContainerdUpgrade:
        f.Register(handler, version, phases.NewEnsureContainerdUpgrade)
    case upgrade.InlineHandlerAgentUpgrade:
        f.Register(handler, version, phases.NewEnsureAgentUpgrade)
    // === 安装 handlers (新增) ===
    case upgrade.InlineHandlerBKEAgentInstall:
        f.Register(handler, version, phases.NewEnsureBKEAgent)
    case upgrade.InlineHandlerNodesEnvInstall:
        f.Register(handler, version, phases.NewEnsureNodesEnv)
    case upgrade.InlineHandlerClusterAPIInstall:
        f.Register(handler, version, phases.NewEnsureClusterAPIObj)
    case upgrade.InlineHandlerCertsInstall:
        f.Register(handler, version, phases.NewEnsureCerts)
    case upgrade.InlineHandlerLoadBalanceInstall:
        f.Register(handler, version, phases.NewEnsureLoadBalance)
    case upgrade.InlineHandlerMasterInitInstall:
        f.Register(handler, version, phases.NewEnsureMasterInit)
    case upgrade.InlineHandlerMasterJoinInstall:
        f.Register(handler, version, phases.NewEnsureMasterJoin)
    case upgrade.InlineHandlerWorkerJoinInstall:
        f.Register(handler, version, phases.NewEnsureWorkerJoin)
    case upgrade.InlineHandlerAddonDeployInstall:
        f.Register(handler, version, phases.NewEnsureAddonDeploy)
    default:
        return fmt.Errorf("unknown inline handler %q", handler)
    }
    return nil
}
```

**注意**：`EnsurePreUpgradeResources` 有特殊的 `ComponentVersion` 注入逻辑（`registry.go:47-56`），安装 handler 无此需求。但 `EnsureBKEAgent` 等安装阶段可能也有类似特殊参数需求，需逐个检查 phase 构造函数签名。如果某些 Phase 构造函数需要额外参数（如 ComponentVersion），需在 `registerInlineComponent`（`registry.go:42-57`）中特殊处理。

### 5.3 安装 Catalog（组件依赖图）

**设计思路**：当前安装顺序由 `DeployPhases` 切片（`list.go:32-44`）硬编码为线性序列。方案 B 需将此序列转化为 DAG 依赖声明。新增 `DeclarativeInstallCatalog` 定义安装组件的依赖关系。

```go
// pkg/upgrade/install_catalog.go 新增

// InstallComponentSpec 定义安装组件规格
// 对称于 UpgradeComponentSpec (catalog.go:40-51)
type InstallComponentSpec struct {
    // Name 是 ReleaseImage 组件名 (VersionContext key 和 DAG 节点名)
    Name    string
    // Version 是组件版本
    Version string
    // Mode 是执行模式: manifest / inline
    Mode UpgradeExecutionMode
    // InlineHandler 是 ComponentFactory handler key (inline 模式)
    InlineHandler string
    // Dependencies 是显式依赖列表 (DAG 边)
    Dependencies []string
}

// DeclarativeInstallCatalog 安装组件目录
// 依赖关系从 DeployPhases (list.go:32-44) 的顺序关系推导:
//   BKEAgent → NodesEnv → ClusterAPIObj → Certs → LoadBalance
//   → MasterInit → MasterJoin → WorkerJoin → AddonDeploy → NodesPostProcess → AgentSwitch
//
// 设计说明:
// - 基础设施组件的依赖从本 catalog 静态表读取 (线性依赖, 保持原有顺序语义)
// - 声明式组件 (coredns/openfuyao-addon 等) 的依赖从 ComponentVersion.Spec.Dependencies 读取
// - 两类依赖通过 MergeDependencyResolver 合并
var DeclarativeInstallCatalog = []InstallComponentSpec{
    {
        Name:          "bkeagent",
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        InlineHandler: InlineHandlerBKEAgentInstall,
        // 无依赖, 第一个执行
    },
    {
        Name:          "nodes-env",
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        InlineHandler: InlineHandlerNodesEnvInstall,
        Dependencies:  []string{"bkeagent"},
    },
    {
        Name:          "cluster-api-obj",
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        InlineHandler: InlineHandlerClusterAPIInstall,
        Dependencies:  []string{"nodes-env"},
    },
    {
        Name:          "certs",
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        InlineHandler: InlineHandlerCertsInstall,
        Dependencies:  []string{"cluster-api-obj"},
    },
    {
        Name:          "load-balance",
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        InlineHandler: InlineHandlerLoadBalanceInstall,
        Dependencies:  []string{"certs"},
    },
    {
        Name:          "kubernetes-master", // 复用 ComponentKubernetesMaster 名
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        InlineHandler: InlineHandlerMasterInitInstall,
        Dependencies:  []string{"load-balance"},
    },
    {
        Name:          "kubernetes-master-join",
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        InlineHandler: InlineHandlerMasterJoinInstall,
        Dependencies:  []string{"kubernetes-master"},
    },
    {
        Name:          "kubernetes-worker",
        Version:       ComponentManifestVersion,
        Mode:          UpgradeExecutionInline,
        InlineHandler: InlineHandlerWorkerJoinInstall,
        Dependencies:  []string{"kubernetes-master-join"},
    },
    // 以下为声明式组件 (来自 install.components, type=helm/yaml)
    // coredns, openfuyao-addon 等的依赖由 ComponentVersion.Spec.Dependencies 声明
    // 通常依赖 kubernetes-worker (集群就绪后才能部署 addon)
    // 不在此静态表中定义, 运行时从 bundle 读取
}

// InstallCatalogDependencyResolver 从安装 catalog 构建依赖解析器
// 仅对 catalog 中定义的基础设施组件返回依赖, 其他组件返回 nil (由 BundleDependencyResolver 处理)
func InstallCatalogDependencyResolver() topology.DependencyResolver {
    catalog := make(map[string][]string)
    for _, spec := range DeclarativeInstallCatalog {
        catalog[spec.Name] = spec.Dependencies
    }
    return func(name, version string) ([]string, error) {
        if deps, ok := catalog[name]; ok {
            return deps, nil
        }
        return nil, nil
    }
}
```

**关键设计决策 — 依赖来源的混合**：

- 基础设施组件（bkeagent/certs/master-init 等）的依赖从 `DeclarativeInstallCatalog` 静态表读取
- 声明式组件（coredns/openfuyao-addon 等）的依赖从 `ComponentVersion.Spec.Dependencies` 动态读取
- `BuildInstallDAGFromBundle` 使用 `MergeDependencyResolver` 合并两者

```go
// pkg/upgrade/bundle.go BuildInstallDAGFromBundle 修订 (方案 B)

func BuildInstallDAGFromBundle(bundle *releasemanifest.Bundle, resolve topology.DependencyResolver) (*topology.UpgradeDAG, error) {
    // 方案 B: 不过滤类型, 所有 install.components 都进 DAG
    components, err := InstallComponentsFromBundle(bundle, nil)
    if err != nil {
        return nil, err
    }
    // 方案 B: 合并 catalog 依赖 (基础设施组件) + bundle 依赖 (声明式组件) + default
    return topology.BuildUpgradeDAG(
        components,
        topology.MergeDependencyResolver(
            resolve,
            InstallCatalogDependencyResolver(),  // 基础设施组件的静态依赖
            topology.DefaultDependencyResolver(),
        ),
    )
}
```

### 5.4 install.components 内容

方案 B 下，`install.components` **必须包含所有组件**——基础设施 + 声明式：

```yaml
spec:
  install:
    components:
      # 基础设施 (inline)
      - name: bkeagent
        version: v1.0.0
      - name: nodes-env
        version: v1.0.0
      - name: cluster-api-obj
        version: v1.0.0
      - name: certs
        version: v1.0.0
      - name: load-balance
        version: v1.0.0
      - name: kubernetes-master
        version: v1.0.0
      - name: kubernetes-master-join
        version: v1.0.0
      - name: kubernetes-worker
        version: v1.0.0
      # 声明式 (helm/yaml)
      - name: coredns
        version: v1.11.1
      - name: openfuyao-addon
        version: v26.03
      - name: kube-proxy
        version: v1.29.0
```

每个组件在 bundle 中必须有对应的 `ComponentVersion` CR，声明 `type`、`inline.handler`（对 inline 类型）、`dependencies`。

**基础设施 ComponentVersion CR 示例**：

```yaml
# bke-manifests/bkeagent/v1.0.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: bkeagent-v1.0.0
spec:
  name: bkeagent
  type: inline
  version: v1.0.0
  inline:
    handler: EnsureBKEAgent
    version: v1.0.0
  # 无 dependencies (第一个执行)
```

```yaml
# bke-manifests/kubernetes-master/v1.0.0/component.yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: kubernetes-master-v1.0.0
spec:
  name: kubernetes-master
  type: inline
  version: v1.0.0
  inline:
    handler: EnsureMasterInit
    version: v1.0.0
  dependencies:
    - name: load-balance
```

### 5.5 控制器流程

方案 B 的 `executeInstallDAG` 与方案 A 基本相同（4.2 节），差异在于：
- **不调用 PhaseFlow DeployPhases**——基础设施组件由 DAG 执行
- **CommonPhases 仍作为前置**（Finalizer/DryRun 不是组件，是控制器逻辑）
- **PostDeployPhases 仍作为后置**

```go
func (r *BKEClusterReconciler) executePhaseFlow(...) (ctrl.Result, error) {
    phaseCtx := ...
    
    if r.shouldUseDeclarativeUpgrade(bkeCluster) {
        ...executeUpgradeDAG...
    }
    
    // 方案 B: Feature Gate ON 时, 全量替换
    if r.isFreshInstall(bkeCluster) && featuregate.HelmComponentEnabled(bkeCluster) {
        // 段 1: CommonPhases (Finalizer/Paused/ClusterManage/DeleteOrReset/DryRun)
        commonFlow := phases.NewPhaseFlow(phaseCtx, phases.WithPhaseSet(phases.CommonPhases))
        if res, err := commonFlow.CalculateAndExecute(); err != nil {
            return res, err
        }
        
        // 段 2: InstallDAG (全部组件: 基础设施 + 声明式)
        if res, err := r.executeInstallDAG(ctx, phaseCtx, oldBkeCluster, bkeCluster, bkeLogger); err != nil {
            return res, err
        }
        
        // 段 3: PostDeployPhases (NodesPostProcess/AgentSwitch/EnsureCluster)
        postFlow := phases.NewPhaseFlow(phaseCtx, phases.WithPhaseSet(phases.PostDeployPhases))
        return postFlow.CalculateAndExecute()
    }
    
    // 旧路径: 全量 PhaseFlow
    flow := phases.NewPhaseFlow(phaseCtx)
    ...
}
```

### 5.6 状态报告映射

升级侧通过 `declarativeUpgradePhaseName`（`bkecluster_upgrade_dag.go:385-396`）将 DAG 节点映射到 `BKEClusterPhase` 用于状态报告。方案 B 需对称的安装映射：

```go
// controllers/capbke/bkecluster_install_dag.go 新增

// declarativeInstallPhaseName 将安装 DAG 节点映射到 BKEClusterPhase 用于状态报告。
// 对称于 declarativeUpgradePhaseName (bkecluster_upgrade_dag.go:385-396)。
func declarativeInstallPhaseName(node *topology.ComponentNode) confv1beta1.BKEClusterPhase {
    if node.Inline != nil && node.Inline.Handler != "" {
        return confv1beta1.BKEClusterPhase(node.Inline.Handler)
    }
    // 非 inline 节点 (helm/yaml) 使用组件名作为 phase 名
    return confv1beta1.BKEClusterPhase(node.Name)
}
```

**状态报告兼容性**：当前 `ClusterInitPhaseNames`（`list.go:88-97`）定义了安装阶段的状态名集合。方案 B 下，DAG 节点的 inline handler 名（如 `EnsureMasterInit`）与 `ClusterInitPhaseNames` 中的值一致，状态报告逻辑无需修改。

## 6. 两方案对比与推荐

### 6.1 对比总览

| 维度 | 方案 A（仅组件层） | 方案 B（全量替换） |
| ------ | ------ | ------ |
| **DAG 覆盖范围** | 仅 Helm/YAML 组件（coredns/addon） | 全部安装阶段（基础设施 + 组件） |
| **install.components 内容** | 仅 helm/yaml 类型 | 全部类型（inline+helm+yaml） |
| **新增 inline handler 注册** | 0 个 | ~11 个（EnsureBKEAgent/MasterInit/WorkerJoin 等） |
| **新增 ComponentVersion CR** | 仅 Helm/YAML 组件 | 全部安装组件（~19 个） |
| **PhaseFlow 改动** | 需支持分段执行（WithPhaseSet） | 需支持分段执行（WithPhaseSet） |
| **依赖声明** | 仅 ComponentVersion.Spec.Dependencies | Catalog 静态表 + ComponentVersion.Spec.Dependencies |
| **迁移成本** | 低（~3 人日） | 高（~10 人日） |
| **风险** | 低（基础设施路径不变） | 高（需验证所有安装阶段在 DAG 中的行为） |
| **并行度收益** | 仅 addon 层可并行 | 基础设施层也可并行（但多为串行依赖） |
| **与 KEP6 范围** | 完全匹配（KEP6 仅设计 Helm/YAML） | 超出 KEP6 范围（需改动基础设施安装） |

### 6.2 技术风险分析

#### 方案 A 风险

1. **双执行模型共存**：PhaseFlow（基础设施）+ DAG（组件）两种执行模型并存，调试时需理解两套机制。风险可控——DAG 仅处理新组件类型，不影响现有基础设施安装。

2. **EnsureAddonDeploy 职责拆分**：当前 `EnsureAddonDeploy`（`ensure_addon_deploy.go`）既部署 addon 又做配置同步。移除 addon 部署逻辑后，需确认配置同步部分是否保留。需检查 `EnsureAddonDeploy` 的完整职责。

3. **install.components 内容假设**：方案 A 假设 `install.components` 仅含 Helm/YAML 组件。如果制品中混入 inline 类型组件，类型过滤会跳过它们——这些组件不会被 DAG 执行，也不会被 PhaseFlow 执行（EnsureAddonDeploy 已移除）。需确保制品规范约束 `install.components` 的内容。

#### 方案 B 风险

1. **Phase 的 `NeedExecute` 复杂逻辑**：安装阶段如 `EnsureMasterInit` 的 `NeedExecute` 包含复杂的集群状态判断（已有 master、证书是否有效等）。DAG 调度器通过 `shouldSkipComponent`（`scheduler.go:276`）检查 `DeclarativeUpgradeStatus.IsCompleted` 跳过——但这与 Phase 自身的 `NeedExecute` 是**两套独立的跳过逻辑**，可能冲突。

2. **Phase 的副作用与重入**：`EnsureMasterInit` 执行 `kubeadm init`，失败后重试需要清理半成品状态。PhaseFlow 的顺序执行天然保证前序 Phase 完成后才执行后续；DAG 批次内并行可能导致未预期的并发副作用（如两个 Phase 同时写 etcd）。

3. **Phase 构造函数签名差异**：`EnsurePreUpgradeResources` 需要 `ComponentVersion` 参数（`registry.go:53-55`），其他 Phase 构造函数签名各不相同。全量注册需逐个适配。

4. **状态报告不兼容**：`DeployPhases` 的状态通过 `ClusterInitPhaseNames`（`list.go:88-97`）等切片报告集群状态。DAG 路径的状态报告通过 `DeclarativeUpgradeStatus.Completed` + `declarativeInstallPhaseName`。两套状态报告机制需统一。

5. **线性依赖的并行度收益有限**：基础设施组件（BKEAgent→NodesEnv→Certs→MasterInit→WorkerJoin）是严格线性依赖，DAG 拓扑排序后仍为单节点批次，无并行收益。真正能并行的是 addon 层（coredns/openfuyao-addon 可并行）——这与方案 A 的并行度相同。

### 6.3 推荐路径

**推荐方案 A 作为最小集，方案 B 作为后续增强**：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              渐进迁移路径                                        │
└─────────────────────────────────────────────────────────────────────────────────┘

  M4 (最小集): 方案 A
  ┌──────────────────────────────────────────────────────────────────┐
  │ • install DAG 仅处理 Helm/YAML 组件                              │
  │ • 基础设施保留 PhaseFlow                                          │
  │ • 验证 DAG + HelmInstaller + YamlInstaller 端到端                 │
  │ • 风险最低, 与 KEP6 范围一致                                      │
  └────────────────────────────┬─────────────────────────────────────┘
                               │
                               │ 验证稳定后
                               ▼
  E4 (增强集): 方案 B 渐进迁移
  ┌──────────────────────────────────────────────────────────────────┐
  │ 步骤 1: EnsureAddonDeploy 组件化 (addon 部署逻辑进 DAG)           │
  │ 步骤 2: EnsureNodesPostProcess / EnsureAgentSwitch 组件化         │
  │ 步骤 3: EnsureMasterInit / EnsureWorkerJoin 组件化                │
  │ 步骤 4: EnsureCerts / EnsureLoadBalance 组件化                    │
  │ 步骤 5: EnsureBKEAgent / EnsureNodesEnv / EnsureClusterAPIObj     │
  │        组件化                                                     │
  │ 最终: DeployPhases 全部进 DAG, PhaseFlow 仅保留 CommonPhases      │
  └──────────────────────────────────────────────────────────────────┘
```

**理由**：

1. 方案 A 与 KEP6 主文档的 Story 4 / M4 任务 4.4（"完整安装流程集成"）范围一致
2. 方案 B 的基础设施并行收益有限（线性依赖为主），但迁移成本和风险显著高于方案 A
3. 方案 A 验证 DAG + HelmInstaller + YamlInstaller 端到端链路后，方案 B 可逐个组件渐进迁移
4. 方案 B 的技术风险（NeedExecute 冲突、副作用并发、构造函数适配）需要充分测试，不适合在最小集中引入

## 7. 实施任务清单

### 7.1 共享任务（两方案都需要）

| # | 任务 | 文件 | 说明 |
| --- | --- | --- | --- |
| S1 | `HasCurrent` + `CurrentVersion` 方法 | `pkg/upgrade/context.go` | KEP 已设计，代码未实现 |
| S2 | `enrichInstallComponent` + `InstallComponentsFromBundle` | `pkg/upgrade/bundle.go` | 对称 enrichUpgradeComponent，支持 filter 参数 |
| S3 | `BuildInstallDAGFromBundle` | `pkg/upgrade/bundle.go` | 对称 BuildDAGFromBundle |
| S4 | `BuildVersionContextForInstall` | `pkg/upgrade/build_release.go` | 对称 BuildVersionContextForUpgrade |
| S5 | `resolveInstallBundle` | `controllers/capbke/bkecluster_install_dag.go` | 对称 resolveUpgradeBundle |
| S6 | `ensureDeclarativeInstallProgress` | `controllers/capbke/bkecluster_install_dag.go` | 复用 DeclarativeUpgradeStatus |
| S7 | `executeInstallDAG` 控制器入口 | `controllers/capbke/bkecluster_install_dag.go` | 对称 executeUpgradeDAG |
| S8 | `executePhaseFlow` 分发扩展 | `controllers/capbke/bkecluster_controller.go` | Feature Gate + isFreshInstall 判断 |
| S9 | `PhaseFlow.WithPhaseSet` 扩展 | `pkg/phaseframe/phases/phase_flow.go` | 支持按子集执行 |
| S10 | `isFreshInstall` 方法 | `controllers/capbke/bkecluster_controller.go` | 判断首次安装 |

### 7.2 方案 A 专属任务

| # | 任务 | 文件 | 说明 |
| --- | --- | --- | --- |
| A1 | `InstallComponentsFromBundle` 类型过滤 | `pkg/upgrade/bundle.go` | filter 参数仅保留 helm/yaml |
| A2 | `DeployPhasesExceptAddon` phase 子集 | `pkg/phaseframe/phases/list.go` | DeployPhases 去除 EnsureAddonDeploy |
| A3 | `executePhaseFlow` 方案 A 分段执行 | `controllers/capbke/bkecluster_controller.go` | Common+Deploy → DAG → PostDeploy |

### 7.3 方案 B 专属任务

| # | 任务 | 文件 | 说明 |
| --- | --- | --- | --- |
| B1 | 安装 inline handler 常量 | `pkg/upgrade/catalog.go` | 11 个 handler 常量 |
| B2 | `registerInlineHandler` 扩展 | `pkg/componentfactory/registry.go` | 注册 11 个安装 handler |
| B3 | `DeclarativeInstallCatalog` | `pkg/upgrade/install_catalog.go` | 安装组件静态依赖表 |
| B4 | `InstallCatalogDependencyResolver` | `pkg/upgrade/install_catalog.go` | catalog 依赖解析器 |
| B5 | `BuildInstallDAGFromBundle` 合并 resolver | `pkg/upgrade/bundle.go` | 合并 catalog + bundle 依赖 |
| B6 | 安装 ComponentVersion CR 模板 | bke-manifests | 为每个安装阶段创建 CV CR |
| B7 | `declarativeInstallPhaseName` 映射 | `controllers/capbke/bkecluster_install_dag.go` | DAG 节点 → BKEClusterPhase |
| B8 | `executePhaseFlow` 方案 B 全量替换 | `controllers/capbke/bkecluster_controller.go` | Common → DAG → PostDeploy |
| B9 | Phase 构造函数签名适配 | `pkg/componentfactory/registry.go` | 检查每个安装 Phase 构造函数参数 |

### 7.4 测试任务

| # | 任务 | 说明 |
| --- | --- | --- |
| T1 | `enrichInstallComponent` 单元测试 | 对称 `bundle_test.go` 中的 upgrade enrich 测试 |
| T2 | `BuildInstallDAGFromBundle` 单元测试 | 验证 install DAG 拓扑结构 |
| T3 | `BuildVersionContextForInstall` 单元测试 | 验证 Current 为空、Target 从 install components 填充 |
| T4 | `HasCurrent` 单元测试 | 验证安装时全部返回 false |
| T5 | `PhaseFlow.WithPhaseSet` 单元测试 | 验证按子集执行正确 |
| T6 | 集成测试：方案 A 安装 DAG 全流程 | coredns (helm) + openfuyao-addon (yaml) 全新安装 |
| T7 | 集成测试：断点续装 | 模拟控制器重启，验证 IsCompleted 跳过已完成组件 |
| T8 | 集成测试：方案 B 全量安装 DAG (增强) | 基础设施 + 声明式组件全流程 |

## 8. 测试设计

### 8.1 单元测试

| 测试模块 | 测试场景 | 覆盖目标 |
| --------- | --------- | --------- |
| **enrichInstallComponent** | bundle 有/无 CV、CV 有/无 Inline、name/version 映射 | >90% |
| **InstallComponentsFromBundle** | 空 bundle、空 install、含 helm/yaml/inline 混合类型、filter 过滤 | >90% |
| **BuildInstallDAGFromBundle** | 单组件、多组件带依赖、循环依赖报错、缺失依赖报错 | >85% |
| **BuildVersionContextForInstall** | Target 填充、Current 为空、HasCurrent 全 false、NeedsUpgrade 全 true | >90% |
| **HasCurrent / CurrentVersion** | nil VC、有/无 Current 记录 | >95% |
| **PhaseFlow.WithPhaseSet** | 全量子集、空子集、CommonPhases only、DeployPhasesExceptAddon | >85% |

#### 8.1.1 enrichInstallComponent 测试用例

```go
// pkg/upgrade/bundle_test.go 新增

func TestEnrichInstallComponent(t *testing.T) {
    tests := []struct {
        name     string
        comp     cvv1alpha1.ReleaseImageInstallComponent
        bundle   *releasemanifest.Bundle
        wantInline *cvv1alpha1.ReleaseImageUpgradeInline
    }{
        {
            name: "bundle has CV with inline",
            comp: cvv1alpha1.ReleaseImageInstallComponent{Name: "coredns", Version: "v1.11.1"},
            bundle: &releasemanifest.Bundle{
                Release: cvv1alpha1.ReleaseImage{},
                Components: map[string]apiv1.ComponentVersion{
                    "coredns@v1.11.1": {
                        Spec: cvv1alpha1.ComponentVersionSpec{
                            Name:    "coredns",
                            Version: "v1.11.1",
                            Type:    cvv1alpha1.ComponentTypeHelm,
                            Inline: &cvv1alpha1.InlineSpec{
                                Handler: "EnsureCoreDNS",
                                Version: "v1.0.0",
                            },
                        },
                    },
                },
            },
            wantInline: &cvv1alpha1.ReleaseImageUpgradeInline{
                Handler: "EnsureCoreDNS",
                Version: "v1.0.0",
            },
        },
        {
            name: "bundle has CV without inline",
            comp: cvv1alpha1.ReleaseImageInstallComponent{Name: "coredns", Version: "v1.11.1"},
            bundle: &releasemanifest.Bundle{
                Components: map[string]apiv1.ComponentVersion{
                    "coredns@v1.11.1": {
                        Spec: cvv1alpha1.ComponentVersionSpec{
                            Type: cvv1alpha1.ComponentTypeHelm,
                            // no Inline
                        },
                    },
                },
            },
            wantInline: nil,
        },
        {
            name: "bundle missing CV",
            comp: cvv1alpha1.ReleaseImageInstallComponent{Name: "missing", Version: "v1.0.0"},
            bundle: &releasemanifest.Bundle{
                Components: map[string]apiv1.ComponentVersion{},
            },
            wantInline: nil,
        },
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := enrichInstallComponent(tt.comp, tt.bundle)
            if tt.wantInline == nil {
                if result.Inline != nil {
                    t.Errorf("expected nil Inline, got %+v", result.Inline)
                }
            } else {
                if result.Inline == nil {
                    t.Fatalf("expected Inline %+v, got nil", tt.wantInline)
                }
                if result.Inline.Handler != tt.wantInline.Handler {
                    t.Errorf("handler: want %q, got %q", tt.wantInline.Handler, result.Inline.Handler)
                }
                if result.Inline.Version != tt.wantInline.Version {
                    t.Errorf("version: want %q, got %q", tt.wantInline.Version, result.Inline.Version)
                }
            }
        })
    }
}
```

#### 8.1.2 BuildVersionContextForInstall 测试用例

```go
// pkg/upgrade/build_release_test.go 新增

func TestBuildVersionContextForInstall(t *testing.T) {
    bundle := &releasemanifest.Bundle{
        Release: cvv1alpha1.ReleaseImage{
            Spec: cvv1alpha1.ReleaseImageSpec{
                Install: &cvv1alpha1.ReleaseImageInstallSpec{
                    Components: []cvv1alpha1.ReleaseImageInstallComponent{
                        {Name: "coredns", Version: "v1.11.1"},
                        {Name: "openfuyao-addon", Version: "v26.03"},
                    },
                },
            },
        },
    }

    vc := BuildVersionContextForInstall(bundle, nil)

    // Target 应从 install.components 填充
    if v := vc.GetTarget("coredns"); v != "v1.11.1" {
        t.Errorf("Target[coredns] = %q, want v1.11.1", v)
    }
    if v := vc.GetTarget("openfuyao-addon"); v != "v26.03" {
        t.Errorf("Target[openfuyao-addon] = %q, want v26.03", v)
    }

    // Current 应为空 (全新安装)
    if vc.HasCurrent("coredns") {
        t.Error("HasCurrent(coredns) = true, want false (fresh install)")
    }
    if vc.HasCurrent("openfuyao-addon") {
        t.Error("HasCurrent(openfuyao-addon) = true, want false (fresh install)")
    }

    // NeedsUpgrade 应全部为 true (有 Target, 无 Current)
    if !vc.NeedsUpgrade("coredns") {
        t.Error("NeedsUpgrade(coredns) = false, want true")
    }
}
```

### 8.2 集成测试

| 测试场景 | 方案 | 验证内容 | 预期结果 |
| --------- | ------ | --------- | --------- |
| **全新安装 (方案 A)** | A | coredns (helm) + openfuyao-addon (yaml) 通过 DAG 安装 | Chart 正确部署，Pod Ready |
| **全新安装 (方案 B)** | B | 全量组件通过 DAG 安装 | 基础设施 + 组件全部就绪 |
| **断点续装** | A+B | 模拟控制器重启 | IsCompleted 跳过已完成组件，继续未完成 |
| **Feature Gate OFF** | A+B | Feature Gate 关闭 | 回退 PhaseFlow，行为不变 |
| **Feature Gate 注解覆盖** | A+B | 全局 OFF + 对象注解 ON | 走 DAG 路径 |

### 8.3 E2E 测试

| 测试场景 | 方案 | 集群规模 | 验证内容 |
| --------- | ------ | --------- | --------- |
| **方案 A 小规模安装** | A | 1 Master + 2 Worker | PhaseFlow (基础设施) + DAG (组件) 全流程 |
| **方案 B 小规模安装** | B | 1 Master + 2 Worker | 全量 DAG 安装全流程 |
| **方案 B 中规模安装** | B | 3 Master + 10 Worker | DAG 并行执行无资源竞争 |
| **安装失败恢复** | A+B | 1 Master + 2 Worker | 模拟组件失败，验证重试/断点续装 |

## 9. 附录

### 9.1 参考文档

- 主文档：`code/kep/kep6/声明式集群版本升级方案-支持 Helm 组件.md`
- 升级 DAG 控制器：`controllers/capbke/bkecluster_upgrade_dag.go:47-129`
- DAG 构建：`pkg/upgrade/bundle.go:24-33` (BuildDAGFromBundle)
- DAG 拓扑：`pkg/topology/build.go:25` (BuildUpgradeDAG)
- VersionContext：`pkg/upgrade/context.go:21-121`
- DeclarativeUpgradeStatus：`api/bkecommon/v1beta1/bkecluster_status.go:86-112`
- Phase 注册：`pkg/phaseframe/phases/list.go:32-44` (DeployPhases)
- Inline handler 注册：`pkg/componentfactory/registry.go:24-40`
- 升级 Catalog：`pkg/upgrade/catalog.go:54-117` (DeclarativeUpgradeCatalog)

### 9.2 文件索引

| 关注点 | 文件:行 |
| --------- | ------ |
| ReleaseImage API 类型 | `api/v1alpha1/releaseimage_types.go:26-72` |
| Install vs Upgrade 组件结构 | `api/v1alpha1/releaseimage_types.go:50-66` |
| ComponentVersion API 类型 | `api/v1alpha1/componentversion_types.go:23-101` |
| Bundle 结构 + ComponentKey | `pkg/release/manifest/types.go:50-91` |
| enrichUpgradeComponent + UpgradeComponentsFromBundle | `pkg/upgrade/bundle.go:36-48, 93-109` |
| BuildDAGFromBundle + BundleDependencyResolver | `pkg/upgrade/bundle.go:24-63` |
| VersionContext + 所有方法 (无 HasCurrent) | `pkg/upgrade/context.go:21-121` |
| BuildVersionContextForUpgrade | `pkg/upgrade/build_release.go:37-81` |
| BuildVersionContextFromBKECluster (legacy) | `pkg/upgrade/build.go:21-49` |
| 组件名常量 | `pkg/upgrade/components.go:16-27` |
| 升级 Catalog | `pkg/upgrade/catalog.go:54-117` |
| 升级 DAG 控制器流程 | `controllers/capbke/bkecluster_upgrade_dag.go:47-129` |
| resolveUpgradeBundle / resolveCurrentReleaseBundle | `controllers/capbke/bkecluster_upgrade_dag.go:182-233` |
| 安装/升级分发 (PhaseFlow vs DAG) | `controllers/capbke/bkecluster_controller.go:214-254` |
| 安装 CV 创建/完成 (无 ReleaseImage 执行) | `controllers/capbke/bkecluster_clusterversion.go:33-105` |
| 安装 Phase 注册 (DeployPhases) | `pkg/phaseframe/phases/list.go:32-44` |
| PhaseFlow Calculate/Execute | `pkg/phaseframe/phases/phase_flow.go:52-279` |
| 安装 Phase 读 ClusterConfig/Addons | `pkg/phaseframe/phases/ensure_addon_deploy.go:132,160,446`; `ensure_cluster.go:411-420` |
| Scheduler ExecuteDAG | `pkg/dagexec/scheduler.go:81-128` |
| Scheduler shouldSkipComponent | `pkg/dagexec/scheduler.go:276-285` |
| Scheduler markComponentCompleted | `pkg/dagexec/scheduler.go:287-300` |
| DeclarativeUpgradeStatus | `api/bkecommon/v1beta1/bkecluster_status.go:86-112` |
| DeclarativeUpgradeStatus.IsCompleted | `api/bkecommon/v1beta1/bkecluster_status.go:190-205` |
| DeclarativeUpgradeStatus.MarkCompleted | `api/bkecommon/v1beta1/bkecluster_status.go:208-221` |
| Inline handler 注册 | `pkg/componentfactory/registry.go:24-40` |
| PhaseRunner.Execute | `pkg/componentfactory/runner.go:28-57` |
| DefaultDependencyResolver (返回 nil) | `pkg/topology/defaults.go:30-38` |
| appendImplicitPreUpgradeDependency | `pkg/upgrade/bundle.go:65-79` |

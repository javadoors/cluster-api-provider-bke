# KEP-22: Legacy PhaseFlow 完全移除方案

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-22 |
| **标题** | Legacy PhaseFlow 完全移除方案 — 全场景 DAG 化 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-09-21 |
| **依赖** | KEP-10 安装 DAG 化设计、KEP-5 声明式升级框架、KEP-18 Condition 条件过滤 |
| **关联** | `kep6/kep10-install-components-declarative-design.md`（安装 DAG 化主文档）、`kep7/kep10-install-dag-with-phaseflow-fallback.md`（PhaseFlow 兜底方案） |

---

## 目录

1. [摘要](#1-摘要)
2. [动机](#2-动机)
3. [设计目标与约束](#3-设计目标与约束)
4. [场景覆盖总览](#4-场景覆盖总览)
5. [纳管已有集群 DAG 化](#5-纳管已有集群-dag-化)
6. [集群扩容 DAG 化](#6-集群扩容-dag-化)
7. [集群删除/重置 DAG 化](#7-集群删除重置-dag-化)
8. [DryRun 模式 DAG 化](#8-dryrun-模式-dag-化)
9. [集群暂停 DAG 化](#9-集群暂停-dag-化)
10. [移除后的执行入口](#10-移除后的执行入口)
11. [工作量评估](#11-工作量评估)
12. [风险与缓解措施](#12-风险与缓解措施)
- [附录](#附录)

---

## 1. 摘要

本提案设计 Legacy PhaseFlow 的完全移除方案，将所有集群生命周期场景（纳管、扩容、删除/重置、DryRun、暂停）从 PhaseFlow 硬编码 Phase 列表迁移到 DAG 驱动路径。当前 BKE 采用双轨执行：安装/升级走 DAG，其他场景走 PhaseFlow 兜底。本提案消除 PhaseFlow 兜底路径，统一所有场景为 DAG 驱动，简化执行入口为场景分发（无 PhaseFlow 回退）。

---

## 2. 动机

### 2.1 现状问题

| 问题 | 说明 | 影响 |
|------|------|------|
| **双轨维护** | 安装/升级走 DAG，纳管/扩容/删除/DryRun/暂停走 PhaseFlow | 两套机制维护成本高，新增组件需改两处 |
| **PhaseFlow 硬编码** | DeployPhases 列表硬编码 11 个 Phase，执行顺序固定 | 新增组件需修改代码，无法声明式声明依赖 |
| **无组件级卸载** | 删除场景通过单体 `EnsureDeleteOrReset` 清理，不按组件逆序卸载 | helm/binary 组件不执行 Uninstall，残留 |
| **扩容无版本决策** | 扩容走 DeployPhases 全量遍历，不利用 VersionContext 过滤 | 已有节点组件被重复检查 |
| **无 Condition 过滤** | PhaseFlow 不支持 KEP-18 Condition 机制 | 无法按操作类型过滤组件 |

### 2.2 目标

将 PhaseFlow 兜底的 5 个场景全部 DAG 化，最终移除 PhaseFlow 代码，统一为 DAG 驱动。

---

## 3. 设计目标与约束

### 3.1 设计目标

1. **全场景 DAG 化**：纳管、扩容、删除/重置、DryRun、暂停 5 个场景全部走 DAG
2. **移除 PhaseFlow**：删除 DeployPhases / PhaseFlow / PhaseStatus 代码
3. **移除 Feature Gate**：`DeclarativeInstallEnabled` 不再需要，DAG 成为唯一路径
4. **统一执行入口**：`reconcileCluster` 场景分发，无 PhaseFlow 回退
5. **组件级卸载**：删除场景按组件逆序卸载（helm uninstall / binary UninstallScript）
6. **Condition 过滤**：所有场景支持 KEP-18 Condition 机制

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后不兼容** | Phase 4 移除后无法回退，必须在 Phase 3 充分验证 |
| **ReleaseImage 必须** | Feature Gate 移除后，ReleaseImage 是唯一版本来源 |
| **离线环境** | ReleaseImage OCI 支持本地缓存，断网安装/升级 |
| **CAPI 兼容** | 不破坏 BKECluster/BKEMachine 与 Cluster API 集成 |

### 3.3 非目标

1. 不实现 Cincinnati/OSUS 式动态升级推荐（后续提案）
2. 不修改 BKECluster/BKENode CRD 的 spec 定义（仅扩展 status）
3. 不实现备份恢复能力（KEP-12 范围）

---

## 4. 场景覆盖总览

| Legacy 场景 | DAG 化方案 | 移除条件 |
|------------|-----------|---------|
| Feature Gate 未启用 | 移除 Feature Gate，DAG 成为唯一路径 | Phase 4 |
| ReleaseImage 无 inline handler | 强制 ReleaseImage 包含 inline 字段 | Phase 4 |
| 纳管已有集群 | 新增 `manage` 组件到安装 DAG + Condition 过滤 | Phase 4 |
| 集群扩容（新增节点） | 复用安装 DAG + `fillCurrentFromExistingNodes` + Condition 过滤 | Phase 4 |
| 集群删除/重置 | 逆序卸载 DAG + `EnsureDeleteOrReset` inline 组件 | Phase 4 |
| DryRun 模式 | `ExecutionContext.DryRun` 标记，DAG 照常遍历但仅打印 | Phase 3 |
| 集群暂停 | 前置检查 `BKECluster.Spec.Pause`，不构建 DAG | Phase 3 |

---

## 5. 纳管已有集群 DAG 化

### 5.1 设计思路

纳管是指将一个已有的 Kubernetes 集群纳入 BKE 管理。与全新安装的核心区别在于：集群已运行，组件已有版本，不能假设 Current 为空。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              纳管 DAG 化设计思路                                                  │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    EnsureClusterManage Phase (独立 Phase)                                       │
│      → 探测已有集群组件版本 → 写入 BKECluster.Status                            │
│      → 后续 Phase 从 Status 读取版本                                             │
│    问题: 纳管 + 安装耦合在一个 PhaseFlow 中，无法利用 DAG 的版本决策             │
│                                                                                 │
│  DAG 化方案:                                                                     │
│    将纳管拆解为 DAG 的第一个组件:                                                │
│      manage 组件 (inline: EnsureClusterManage)                                   │
│        → 仅纳管场景执行 (Condition: '{{ eq .Operation "manage" }}')             │
│        → 探测版本 → 填充 VersionContext.Current                                 │
│        → 后续组件通过 VersionContext.Decide() 判断:                             │
│          Current == Target → DecisionSkip (已有正确版本，跳过)                   │
│          Current != Target → DecisionUpgrade (需要升级到目标版本)               │
│          Current == "" → DecisionInstall (缺失组件，需要安装)                    │
│        → 全新安装场景: Condition 求值 false → 跳过 manage → Current 保持空     │
│          → 后续组件全部 DecisionInstall (全新安装)                               │
│                                                                                 │
│  核心优势:                                                                       │
│  1. 版本感知 — 纳管后仅安装/升级缺失或版本不符的组件，不重复安装已有组件        │
│  2. 断点续传 — DeclarativeUpgradeStatus 追踪已完成组件，中断后恢复              │
│  3. 并行执行 — 纳管后的安装/升级走 DAG 并行批次，而非 PhaseFlow 串行            │
│  4. 统一路径 — 纳管 + 安装 + 升级统一走 DAG，消除 PhaseFlow 特殊路径            │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 Operation 填充到 TemplateContext

`Condition: '{{ eq .Operation "manage" }}'` 需要从 `TemplateContext` 读取 `.Operation` 字段。`Operation` 是调用上下文信息，由调用方在构建 `ExecutionContext` 后显式设置：

| 调用方 | Operation | ScaleType |
|--------|-----------|-----------|
| `executeInstallDAG` | `"install"` | `""` |
| `executeManageDAG` | `"manage"` | `""` |
| `executeScaleDAG` | `"scale"` | `"master"` / `"worker"` |
| `executeUpgradeDAG` | `"upgrade"` | `""` |
| `executeUninstallDAG` | `"rollback"` | `""` |

```go
execCtx := buildExecutionContext(phaseCtx, oldCluster, newCluster, bkeLogger, nil)
execCtx.TemplateContext.Operation = "manage"  // ★ 调用方显式设置
```

### 5.3 代码实现

```go
// controllers/capbke/bkecluster_controller.go — 纳管场景入口

func (r *BKEClusterReconciler) executeManageDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 1. 解析 ReleaseImage bundle
    bundle, err := r.resolveInstallBundle(ctx, newCluster)

    // 2. 构建纳管 VersionContext (Current 初始为空，由 manage 组件填充)
    vc := upgrade.NewVersionContext()
    upgrade.FillTargetFromBundle(vc, bundle)
    phaseCtx.SetVersionContext(vc)

    // 3. 构建 DAG (包含所有 install.components，包括 manage)
    dag, err := upgrade.BuildInstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))

    // 4. 构建 ComponentFactory (注册 manage handler)
    factory := componentfactory.NewFactoryFromBundle(bundle)
    factory.RegisterInstallHandlers()

    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:       NewInlinePhaseRunnerAdapter(phaseCtx, &PhaseRunner{Factory: factory}),
        ManifestStore:      manifest.NewBundleStore(bundle),
        CVStore:            manifest.NewBundleStore(bundle),
        MaxParallelPerBatch: 1, // ★ 首个 Batch 串行 (manage 先执行填充 Current)
    })

    execCtx := buildExecutionContext(ctx, r.Client, newCluster, vc)
    execCtx.TemplateContext.Operation = "manage"

    // 5. 执行 DAG — shouldExecuteByCondition 在跳过链 (C) 层生效
    //    Batch 1: manage (Condition 求值 true) → 探测版本 → 填充 VC.Current
    //    Batch 2+: 后续组件 Decide: Skip/Upgrade/Install
    return sched.ExecuteDAG(ctx, execCtx, dag)
}
```

### 5.4 纳管场景判断

```go
func (r *BKEClusterReconciler) isManage(bkeCluster *bkev1beta1.BKECluster) bool {
    return bkeCluster.Spec.Manage
}
```

纳管优先于扩容和升级，因为纳管时先探测版本再决定后续操作。

---

## 6. 集群扩容 DAG 化

### 6.1 设计思路

扩容是指向已有集群新增 Master 或 Worker 节点。核心区别：已有节点不需要重新安装，仅新节点需要执行安装操作。

采用**三层机制**：

```txt
第一层: Scheduler 组件级 Decision (VersionContext + Condition)
  (A) shouldSkipComponent → DeclarativeUpgradeStatus.IsCompleted → Skip
  (B) componentNeedsUpgrade → VersionContext.NeedsExecution:
      集群级组件: Current==Target → Skip (已安装)
      节点级组件: Current=="" → Install (执行)
  (C) shouldExecuteByCondition → EvaluateCondition(cv.Spec.Condition, tmpl):
      kubernetes-master: '{{ eq .ScaleType "master" }}'
        Master 扩容 → true → 执行; Worker 扩容 → false → Skip
  (D) executeComponent → 分发到 Executor

第二层: PhaseRunner.NeedExecute() 前置守卫 (StateCode)
  NeedExecute=true → 有节点位标记未设置 → 继续 Execute
  NeedExecute=false → 所有节点位标记已设置 → return nil

第三层: inline handler 节点级过滤 (StateCode 位标记)
  已有节点: NodeEnvFlag/NodeBootFlag 已设置 → 跳过
  新增节点: 位标记未设置 → 执行安装
```

### 6.2 Legacy PhaseFlow 扩容执行机制

PhaseFlow 遍历完整 DeployPhases（11 个 Phase），每个 Phase 的 `NeedExecute()` 检查 `BKENode.Status.StateCode` 位标记。`ClusterScaleMasterUpPhaseNames` 等列表仅用于状态上报（设置 ClusterStatus），不用于执行决策。

### 6.3 代码实现

```go
func (r *BKEClusterReconciler) executeScaleDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    bkeLogger *bkev1beta1.BKELogger,
) (bool, ctrl.Result, error) {
    // 1. 解析当前版本 ReleaseImage bundle
    currentVersion, err := r.clusterCurrentOpenFuyaoVersion(ctx, newCluster)
    bundle, _, err := r.resolveUpgradeBundle(ctx, newCluster, currentVersion)

    // 2. 构建安装 VersionContext
    vc := upgrade.BuildVersionContextForInstall(bundle)
    r.fillCurrentFromExistingNodes(ctx, newCluster, vc)
    phaseCtx.SetVersionContext(vc)

    // 3. 构建安装 DAG
    dag, err := upgrade.BuildInstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))

    // 4. 状态上报
    if err := r.patchClusterStatus(newCluster, bkev1beta1.ClusterMasterScalingUp); err != nil {
        return false, ctrl.Result{}, err
    }

    // 5. 构建 ComponentFactory
    factory, err := componentfactory.NewFactoryFromBundle(bundle)
    factory.RegisterInstallHandlers()

    // 6. 构建 Scheduler
    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:    NewInlinePhaseRunnerAdapter(phaseCtx, &componentfactory.PhaseRunner{Factory: factory}),
        ManifestStore:   manifest.NewBundleStore(bundle),
        CVStore:         manifest.NewBundleStore(bundle),
        MaxParallelPerBatch: 8,
    })

    // 7. 构建 ExecutionContext
    execCtx := buildExecutionContext(phaseCtx, oldCluster, newCluster, bkeLogger, nil)
    execCtx.TemplateContext.Operation = "scale"
    execCtx.TemplateContext.ScaleType = inferScaleType(oldCluster, newCluster)

    // 8. 执行 DAG
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        return false, ctrl.Result{}, fmt.Errorf("execute scale DAG: %w", err)
    }

    newCluster.Status.ClusterStatus = bkev1beta1.ClusterStatusReady
    return true, ctrl.Result{}, nil
}

// fillCurrentFromExistingNodes 从当前版本 ReleaseImage 填充集群级组件 Current
// 节点级组件 Current 保持为空 (使三层机制生效)
func (r *BKEClusterReconciler) fillCurrentFromExistingNodes(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    vc *upgrade.VersionContext,
) {
    currentVersion, err := r.clusterCurrentOpenFuyaoVersion(ctx, bkeCluster)
    if err != nil || currentVersion == "" {
        return
    }
    currentBundle, err := r.resolveCurrentReleaseBundle(ctx, bkeCluster, currentVersion)
    if err != nil || currentBundle == nil {
        return
    }
    upgrade.FillCurrentFromBundle(vc, currentBundle)
    // 清除节点级组件的 Current (保持为空, 使三层机制生效)
    for _, name := range vc.TargetNames() {
        if isNodeScopedComponent(name) {
            vc.SetCurrent(name, "")
        }
    }
}
```

### 6.4 扩容触发流程示例

以 3 节点集群扩容新增 master-2 为例：

```
executeScaleDAG:
  1. fillCurrentFromExistingNodes 填充 VersionContext:
     集群级组件: Current==Target → Skip
     节点级组件: Current=="" → Install
  1a. 设置 Operation="scale", ScaleType="master"

  2. Scheduler.ExecuteDAG:
  第一层: (B) 集群级 Current==Target → Skip; 节点级 Current=="" → Install
         (C) kubernetes-master: ScaleType="master" → true → 执行
             kubernetes-worker: ScaleType="master" → false → Skip
  第二层: NeedExecute(StateCode) → master-2 位标记未设置 → true
  第三层: filterNodes → 已有节点跳过, master-2 执行
```

---

## 7. 集群删除/重置 DAG 化

### 7.1 设计思路

删除/重置是指清理集群的所有组件。与安装的核心区别在于：安装是创建组件，删除是逆序卸载组件。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              删除/重置 DAG 化设计思路                                              │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    DeletePhases (仅 2 个 Phase):                                                 │
│      1. EnsurePaused — 暂停集群操作                                              │
│      2. EnsureDeleteOrReset — 单体删除逻辑 (含全部清理操作)                      │
│                                                                                 │
│    EnsureDeleteOrReset.Execute() 内部执行:                                       │
│      handleClusterDeletion → 删除 CAPI Cluster (级联删除 Machine)                │
│      handleBKEMachineDeletion → 等待 BKEMachine 删除完成                        │
│      deleteRelatedResources → 删除 Secret/Command                               │
│      ShutDownAgent → SSH 关闭节点上的 bkeagent                                  │
│      cleanupClusterResources → 删除 BKENode/Event/finalizer                    │
│      handleNamespaceDeletion → 删除命名空间                                      │
│                                                                                 │
│    问题: 全部清理逻辑耦合在一个 Phase 中, 无组件级卸载:                          │
│    1. 无逆序卸载: 不区分组件类型, 不按依赖逆序卸载                               │
│    2. 无组件级清理: CAPI 级联删除 Machine, 但节点上组件不被显式卸载               │
│    3. 无 helm 卸载: 不执行 helm uninstall                                       │
│    4. 无 binary 卸载: 不执行 UninstallScript, 仅关闭 bkeagent 进程               │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.2 三种移除方案

**方案 A: 完整逆序 DAG**

构建卸载 DAG（安装 DAG 的逆序），按组件依赖逆序卸载每个组件：

```txt
卸载 DAG 拓扑 (逆序):

  Batch 1: [agent-switch]           → 停止 Agent 监听
  Batch 2: [kube-proxy, coredns]    → helm uninstall / kubectl delete
  Batch 3: [kubernetes-worker]      → kubeadm reset (worker)
  Batch 4: [kubernetes-master]      → kubeadm reset (master)
  Batch 5: [load-balance]           → 删除 HA
  Batch 6: [certs]                 → 删除证书
  Batch 7: [containerd]            → SSH UninstallScript (停止服务 + 清理二进制)
  Batch 8: [bkeagent]              → SSH Shutdown (关闭进程)
  Batch 9: [delete-cluster-resources] ★ 管理集群资源清理
```

各组件类型的卸载方式：

| 组件类型 | 卸载方式 |
|---------|---------|
| inline | 调用 handler 的 Uninstall/Reset 逻辑 (如 kubeadm reset) |
| yaml | YamlInstaller.DeleteComponent (kubectl delete) |
| helm | HelmInstaller.Uninstall (helm uninstall) |
| binary | BinaryInstaller.Uninstall (SSH UninstallScript, 停止服务 + 清理二进制) |

**方案 B: EnsureDeleteOrReset 注册为 inline 组件（拆解为 DAG 依赖终点）**

将 Legacy `EnsureDeleteOrReset` 的管理集群清理逻辑封装为 inline 组件 `delete-cluster-resources`，作为卸载 DAG 的**最后一个组件**（依赖终点）。目标集群组件卸载由前序 DAG 组件完成。

**方案 C: EnsureDeleteOrReset 原样注册为 inline 组件（最小迁移）**

`EnsureDeleteOrReset` 的 `reconcileDelete` 全部逻辑**零改动**，直接注册为 inline 组件。DAG 仅含 `delete-cluster-resources` 一个节点，不构建逆序卸载 DAG，不执行组件级卸载。行为与 Legacy 完全一致。

### 7.3 三方案对比

| 维度 | 方案 A: 完整逆序 DAG | 方案 B: 拆解为 inline 组件 | 方案 C: 原样注册 ★ |
|------|---------------------|------------------------|-------------------|
| DAG 节点数 | 全部组件 (8+ 节点) | 全部组件 + delete-cluster-resources (9 节点) | 仅 1 个节点 (代码硬编码) |
| EnsureDeleteOrReset 改造 | 裁剪为仅管理集群清理 | 裁剪为仅管理集群清理 | **零改动** |
| 依赖 ReleaseImage | ✅ 需要 (构建逆序 DAG) | ✅ 需要 (构建逆序 DAG) | **❌ 不依赖** (代码硬编码 DAG) |
| 目标集群组件卸载 | 逐组件 Uninstall (yaml/helm/binary/inline) | 逐组件 Uninstall + 管理集群清理 | 不卸载 (依赖 CAPI 级联, 同 Legacy) |
| helm 卸载 | ✅ helm uninstall | ✅ helm uninstall | ❌ (同 Legacy, 不卸载) |
| binary 卸载 | ✅ SSH UninstallScript | ✅ SSH UninstallScript | ❌ (同 Legacy, 仅 ShutdownAgent) |
| 与 Legacy 行为一致性 | ❌ 增强 (新增卸载) | ❌ 增强 (新增卸载) | ✅ **完全一致** |
| 迁移风险 | 高 (新增 Uninstall 逻辑) | 高 (拆解 + 新增 Uninstall) | **低** (零代码改动) |
| 适用阶段 | Phase 4 (全量 DAG) | Phase 4 (全量 DAG) | **Phase 2-3 (灰度迁移)** |

### 7.4 方案 C 代码实现（最小迁移，当前采用）

```go
func (r *BKEClusterReconciler) executeUninstallDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 1. 直接在代码中构建单节点 DAG (delete-cluster-resources)
    //    不依赖 ReleaseImage (与集群版本无关的基础设施操作)
    dag := topology.NewUpgradeDAG()
    dag.AddNode(&topology.ComponentNode{
        Name:          "delete-cluster-resources",
        Version:       "v1.0.0",
        FailurePolicy: topology.FailurePolicyFailFast,
        Inline: &topology.InlineRef{
            Handler: "EnsureDeleteOrReset",
            Version: "v1.0.0",
        },
    })

    // 2. 构建 ComponentFactory
    factory := componentfactory.NewComponentFactory()
    factory.Register("EnsureDeleteOrReset", "v1.0.0", phases.NewEnsureDeleteOrReset)

    // 3. 构建 Scheduler
    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:    NewInlinePhaseRunnerAdapter(phaseCtx, &PhaseRunner{Factory: factory}),
        CVStore:         factory,
        MaxParallelPerBatch: 1,
    })

    // 4. 构建 ExecutionContext
    execCtx := buildExecutionContext(phaseCtx, oldCluster, newCluster, bkeLogger, nil)
    execCtx.TemplateContext.Operation = "rollback"

    // 5. 执行 DAG (单节点)
    //    EnsureDeleteOrReset.Execute() → reconcileDelete() 全部逻辑 (与 Legacy 完全一致)
    return sched.ExecuteDAG(ctx, execCtx, dag)
}
```

**设计原则**：`delete-cluster-resources` 不注册到 ReleaseImage `install.components` 中，直接在代码中硬编码。原因：
1. **与集群版本无关**：`EnsureDeleteOrReset` 的 `reconcileDelete` 逻辑是基础设施操作，不随 ReleaseImage 版本变化
2. **避免 ReleaseImage 污染**：`install.components` 语义是"安装到目标集群的组件"，`delete-cluster-resources` 是管理集群清理操作
3. **减少外部依赖**：不依赖 ReleaseImage bundle 解析，删除场景不需要 ReleaseImage 就绪即可执行（与 Legacy DeletePhases 行为一致）

### 7.5 EnsureDeleteOrReset 零改动

```go
// pkg/phaseframe/phases/ensure_delete_or_reset.go — 现有代码, 不修改

func (e *EnsureDeleteOrReset) Execute() (ctrl.Result, error) {
    baseCtx := context.Background()
    _, c, bkeCluster, _, log := e.Ctx.Untie()

    // 1. 如果集群暂停, 先恢复并清除所有 Command (现有逻辑, 不变)
    if e.Ctx.BKECluster.Spec.Pause { ... }

    // 2. 轮询执行 reconcileDelete (超时 60 分钟) (现有逻辑, 不变)
    err := wait.PollImmediateUntil(..., func() (bool, error) {
        return e.reconcileDelete(ctx) == nil, nil
    }, ctx.Done())
}

func (e *EnsureDeleteOrReset) reconcileDelete(ctx context.Context) error {
    // ★ 全部逻辑保持不变:
    e.ensureClusterStatusDeleting(...)   // 设置 ClusterStatus = ClusterDeleting
    e.handleClusterDeletion(...)         // 删除 CAPI Cluster (级联删除 Machine)
    e.handleBKEMachineDeletion(...)      // 等待 BKEMachine 删除完成
    e.deleteRelatedResources(...)        // 删除 Secret/Command
    e.ShutDownAgent(ctx)                 // SSH 关闭 bkeagent
    e.cleanupClusterResources(...)       // 删除 BKENode/Event/finalizer
    e.handleNamespaceDeletion(...)       // 删除命名空间
}

// NeedExecute — 现有代码, 不修改
func (e *EnsureDeleteOrReset) NeedExecute(_ *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
    if !new.DeletionTimestamp.IsZero() || new.Spec.Reset {
        e.SetStatus(bkev1beta1.PhaseWaiting)
        return true
    }
    return false
}
```

### 7.6 与 Legacy DeletePhases 的能力一致性

| `reconcileDelete` 步骤 | Legacy DeletePhases | 方案 C: DAG inline 组件 | 一致性 |
|------------------------|--------------------|-----------------------|--------|
| `ensureClusterStatusDeleting` | ✅ 在 `reconcileDelete` 中 | ✅ 在 `reconcileDelete` 中 (零改动) | ✅ 一致 |
| `handleClusterDeletion` | ✅ 删除 CAPI Cluster | ✅ 同上 | ✅ 一致 |
| `handleBKEMachineDeletion` | ✅ 等待 BKEMachine 删除 | ✅ 同上 | ✅ 一致 |
| `deleteRelatedResources` | ✅ 删除 Secret/Command | ✅ 同上 | ✅ 一致 |
| `ShutDownAgent` | ✅ SSH 关闭 bkeagent | ✅ 同上 | ✅ 一致 |
| `cleanupClusterResources` | ✅ 删除 BKENode/Event/finalizer | ✅ 同上 | ✅ 一致 |
| `handleNamespaceDeletion` | ✅ 删除命名空间 | ✅ 同上 | ✅ 一致 |
| 暂停恢复 (Spec.Pause) | ✅ Execute() 入口检查 | ✅ 同上 (零改动) | ✅ 一致 |
| 超时重试 (60 分钟轮询) | ✅ `wait.PollImmediateUntil` | ✅ 同上 | ✅ 一致 |
| 触发条件 | `DeletionTimestamp` / `Spec.Reset` | `Condition` + `NeedExecute(DeletionTimestamp)` | ✅ 一致 (双守卫) |

### 7.7 后续升级路径（方案 C → 方案 A/B）

方案 C 是最小迁移方案，后续可在 Phase 4 升级为方案 A/B：

1. **Phase 2-3**：方案 C（原样注册，单节点 DAG，零代码改动，与 Legacy 完全一致）
2. **Phase 4**：升级为方案 A（完整逆序 DAG，逐组件 Uninstall，新增 helm/binary 卸载）或方案 B（拆解 EnsureDeleteOrReset，管理集群清理作为 DAG 依赖终点）

> **设计优势**：方案 C 零代码改动，`EnsureDeleteOrReset` 的 `reconcileDelete` 全部逻辑原样复用。迁移仅涉及执行入口切换（PhaseFlow → DAG），行为与 Legacy 完全一致，风险最低。

---

## 8. DryRun 模式 DAG 化

### 8.1 设计思路

DryRun 是指仅模拟执行，不实际修改集群。DAG 照常构建和遍历，但各执行器检查 `DryRun` 标记后仅打印不执行。

```go
// pkg/dagexec/execution_context.go — 新增 DryRun 字段

type ExecutionContext struct {
    // ... 现有字段 ...
    DryRun       bool   // DryRun 模式标记
    UninstallMode bool  // 卸载模式标记
}
```

### 8.2 代码实现

```go
// executeDryRunDAG — 与 executeInstallDAG 逻辑相同，仅设置 DryRun 标记
func (r *BKEClusterReconciler) executeDryRunDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 1-6. 与 executeInstallDAG 完全相同
    // ...

    // 7. 构建 ExecutionContext
    execCtx := buildExecutionContext(phaseCtx, oldCluster, newCluster, bkeLogger, targetClient)
    execCtx.TemplateContext.Operation = "install"

    // ★ 唯一区别: 设置 DryRun 标记
    execCtx.DryRun = true

    // 8. 执行 DAG — 各执行器检查 DryRun 后仅打印不执行
    return sched.ExecuteDAG(ctx, execCtx, dag)
}
```

### 8.3 Scheduler DryRun 检查

```go
func (s *Scheduler) executeComponent(...) error {
    // ★ DryRun 检查
    if execCtx.DryRun {
        decision := upgrade.Decide(execCtx.VersionContext, node.Name)
        log.Info("[DryRun] component execution plan",
            "component", node.Name, "version", node.Version,
            "decision", decision, "dependencies", node.Dependencies)
        return nil // 不实际执行
    }
    // 实际执行 (现有逻辑)
}
```

---

## 9. 集群暂停 DAG 化

### 9.1 设计思路

暂停不需要 DAG 化 — 它的语义是"不执行任何操作"。在 `reconcileCluster` 入口处前置检查即可。

```go
// 暂停检查 (最优先，在任何 DAG 构建之前)
if newCluster.Spec.Pause {
    newCluster.Status.ClusterStatus = bkev1beta1.ClusterPaused
    return ctrl.Result{}, nil // 不执行任何操作
}
```

---

## 10. 移除后的执行入口

完全移除 Legacy PhaseFlow 后，执行入口简化为场景分发（无 PhaseFlow 回退）：

```go
func (r *BKEClusterReconciler) reconcileCluster(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    // ─── 前置检查 (最优先) ───
    if newCluster.Spec.Pause {
        newCluster.Status.ClusterStatus = bkev1beta1.ClusterPaused
        return ctrl.Result{}, nil
    }

    // ─── 场景判断 (无 Legacy 回退) ───
    switch {
    case isDeleteOrReset(newCluster):
        return ctrl.Result{}, r.executeUninstallDAG(ctx, phaseCtx, oldCluster, newCluster)

    case isManage(newCluster):
        return ctrl.Result{}, r.executeManageDAG(ctx, phaseCtx, oldCluster, newCluster)

    case isDryRun(newCluster):
        return ctrl.Result{}, r.executeDryRunDAG(ctx, phaseCtx, oldCluster, newCluster)

    case isScale(oldCluster, newCluster):
        return ctrl.Result{}, r.executeScaleDAG(ctx, phaseCtx, oldCluster, newCluster)

    case r.shouldUseDeclarativeUpgrade(newCluster):
        return ctrl.Result{}, r.executeUpgradeDAG(ctx, phaseCtx, oldCluster, newCluster)

    default:
        return ctrl.Result{}, r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    }
    // 不再有 PhaseFlow 回退
}
```

**场景判断优先级**：

| 优先级 | 场景 | 判断条件 | 执行路径 | 说明 |
|--------|------|---------|---------|------|
| 0 | 暂停 | `Spec.Pause` | 不执行 | 最优先 |
| 1 | 删除/重置 | `Spec.Reset` 或 `DeletionTimestamp` | 卸载 DAG | 优先于其他操作 |
| 2 | 纳管 | `Spec.Manage` | 纳管 DAG | 先探测版本再决定 |
| 3 | DryRun | `Spec.DryRun` | 安装 DAG (DryRun) | 优先于扩容/升级 |
| 4 | 扩容 | 新增节点 | 安装 DAG (过滤已有节点) | 优先于升级 |
| 5 | 升级 | `upgrade-ready` annotation | 升级 DAG | — |
| 6 | 安装 | 默认 | 安装 DAG | 兜底 |

---

## 11. 工作量评估

| 类别 | 模块 | 估算（人天） |
|------|------|------------|
| 开发 | 纳管 DAG 化 (§5) | `manage` 组件 + Condition 过滤 + ClusterVersion 协调 | 5 |
| 开发 | 扩容 DAG 化 (§6) | `executeScaleDAG` + `fillCurrentFromExistingNodes` + 三层机制 | 4 |
| 开发 | 删除/重置 DAG 化 (§7) | `executeUninstallDAG` + EnsureDeleteOrReset inline 组件 | 4 |
| 开发 | DryRun DAG 化 (§8) | `ExecutionContext.DryRun` + 各执行器适配 | 2 |
| 开发 | 暂停检查 (§9) | 前置检查 | 1 |
| 开发 | 执行入口重写 (§10) | `reconcileCluster` 场景分发 | 3 |
| 开发 | PhaseFlow 代码清理 | 移除 DeployPhases / PhaseFlow / PhaseStatus | 5 |
| 开发 | Feature Gate 移除 | 移除 `DeclarativeInstallEnabled` | 1 |
| 开发 | 回退预案 | Phase 3 验证清单 + 手动恢复方案 | 2 |
| 测试 | 全场景 E2E + 回归 + 性能对比 | 11 |
| **合计** | | **~38 人天** |

---

## 12. 风险与缓解措施

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| Phase 4 无回退 | 移除 PhaseFlow 后发现问题无法回退 | 高 | Phase 3 充分验证 + 灰度周期足够长 |
| 纳管探测失败 | manage 组件探测版本失败，干扰后续 Decide | 中 | Condition 过滤确保仅纳管场景执行 manage |
| 扩容 Condition 误过滤 | ScaleType 设置错误导致组件误跳过 | 中 | 单元测试覆盖 Master/Worker 扩容场景 |
| 删除 DAG 单节点不完整 | EnsureDeleteOrReset 逻辑未覆盖所有清理 | 低 | 零改动复用现有 reconcileDelete，与 Legacy 行为一致 |
| DryRun 输出不完整 | DryRun 模式下某些执行器未检查标记 | 低 | 统一在 Scheduler.executeComponent 层检查 |

---

## 附录

### A. 参考文档

1. [KEP-10 安装 DAG 化设计](../kep6/kep10-install-components-declarative-design.md)
2. [KEP-10 安装 DAG（PhaseFlow 兜底方案）](kep10-install-dag-with-phaseflow-fallback.md)
3. [KEP-5 声明式升级框架](../kep5/kep5.md)
4. [KEP-18 ComponentVersion 执行时条件过滤](../kep6/kep18-component-condition-filter-design.md)

### B. 术语表

| 术语 | 定义 |
|------|------|
| **PhaseFlow** | Legacy 执行路径，通过硬编码 DeployPhases 列表顺序执行 Phase |
| **DAG 路径** | 声明式执行路径，通过 ReleaseImage 构建 DAG 拓扑排序执行 |
| **三层机制** | VersionContext (组件级) + NeedExecute (前置守卫) + filterNodes (节点级过滤) |
| **Condition** | ComponentVersion.Spec.Condition，Go Template 表达式，执行时求值决定组件是否执行 |
| **Operation** | TemplateContext 中的操作类型字段 (install/upgrade/scale/manage/rollback)，由调用方设置 |
| **manage 组件** | inline 组件，探测已有集群版本并填充 VersionContext.Current |
| **delete-cluster-resources** | inline 组件，复用 EnsureDeleteOrReset 的管理集群资源清理逻辑 |

---

**文档版本**: v1.0
**维护者**: openFuyao Team

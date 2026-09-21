# KEP-10: Legacy PhaseFlow 完全移除方案

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-10 (Legacy PhaseFlow 移除方案) |
| **标题** | Legacy PhaseFlow 完全移除方案 |
| **状态** | provisional |
| **类型** | Feature |
| **来源** | 从 kep6/kep10-install-components-declarative-design.md §10 抽离 |

---

## 目录

10.1 场景覆盖总览
10.2 纳管已有集群 DAG 化
10.3 集群扩容 DAG 化
10.4 集群删除/重置 DAG 化
10.5 DryRun 模式 DAG 化
10.6 集群暂停 DAG 化
10.7 移除后的执行入口

---

## 10. Legacy PhaseFlow 完全移除方案

当迁移到 Phase 4 时，需要完全移除 Legacy PhaseFlow 路径。以下针对 7.1 节中列出的每个 Legacy 场景，给出 DAG 化的完整方案。

### 10.1 场景覆盖总览

| Legacy 场景 | DAG 化方案 | 移除条件 |
|------------|-----------|---------|
| Feature Gate 未启用 | 移除 Feature Gate，DAG 成为唯一路径 | Phase 4 |
| ReleaseImage 无 inline handler | 强制 ReleaseImage 包含 inline 字段 | Phase 4 |
| 无 install-ready annotation | 已移除: Feature Gate 开启即代表 ReleaseImage 就绪 | 已移除 |
| 纳管已有集群 | 新增 `manage` 组件到安装 DAG | Phase 4 |
| 集群扩容（新增节点） | 新增 `scale-master` / `scale-worker` 组件到 DAG | Phase 4 |
| 集群删除/重置 | 新增 `delete` DAG（逆序卸载） | Phase 4 |
| DryRun 模式 | DAG 执行器支持 DryRun 标记 | Phase 3 |
| 集群暂停 | DAG 前置检查 `BKECluster.Spec.Pause` | Phase 3 |

### 10.2 纳管已有集群 DAG 化

#### 设计思路

纳管是指将一个已有的 Kubernetes 集群纳入 BKE 管理。与全新安装的核心区别在于：集群已运行，组件已有版本，不能假设 Current 为空。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              纳管 DAG 化设计思路                                                  │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    EnsureClusterManage Phase (独立 Phase)                                       │
│      → 探测已有集群组件版本 (kubectl get / NodeInfo / Pod image)                │
│      → 写入 BKECluster.Status (KubernetesVersion/EtcdVersion/...)              │
│      → 后续 Phase 从 Status 读取版本                                             │
│    问题: 纳管 + 安装耦合在一个 PhaseFlow 中，无法利用 DAG 的版本决策和断点续传  │
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
│  manage 组件的条件包含:                                                          │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 问题: manage 组件放在 install.components 中，对全新安装有没有影响？      │   │
│  │                                                                         │   │
│  │ 影响:                                                                   │   │
│  │ • 全新安装: manage 探测不存在的集群 → 探测失败/空版本 → 干扰后续 Decide │   │
│  │ • 纳管场景: manage 探测已有集群 → 填充 VC.Current → 后续组件正确 Decide │   │
│  │                                                                         │   │
│  │ 解决方案: Condition 执行时过滤 (KEP-18)                                 │   │
│  │ • manage 在 ReleaseImage install.components 中 (声明式定义)             │   │
│  │ • DAG 构建时包含所有 install.components (不过滤)                        │   │
│  │ • VersionContext 构建时不过滤 (Current 全空, Target 全填充):           │   │
│  │   - 全新安装和纳管均用 FillTargetFromBundle(vc, bundle)               │   │
│  │     → manage 的 target!="" → Decide() 返回 DecisionInstall            │   │
│  │ • 执行时 Condition 过滤 (shouldExecuteByCondition):                    │   │
│  │   - manage 的 ComponentVersion.Spec.Condition:                         │   │
│  │     '{{ eq .Operation "manage" }}'                                      │   │
│  │   - 全新安装: Operation="install" → 求值 "false" → Skip (跳过 manage) │   │
│  │   - 纳管场景: Operation="manage" → 求值 "true" → 执行 manage 探测     │   │
│  │                                                                         │   │
│  │ 为什么用 Condition 过滤而非 VersionContext 构建时过滤:                  │   │
│  │ • VersionContext 构建时过滤: 需传递 excludeComponents 参数，增加 API 复杂度 │   │
│  │ • Condition 过滤: 统一由 shouldExecuteByCondition 控制，职责清晰       │   │
│  │ • 与 §6.4 简化设计一致: VC 构建时直接包含所有组件，不过滤               │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  Operation 填充到 TemplateContext (KEP-18 §3):                                   │
│  ┌─────────────────────────────────────────────────────────────────────────┐   │
│  │ 问题: Condition '{{ eq .Operation "manage" }}' 需要从 TemplateContext   │   │
│  │       读取 .Operation 字段, 但 BKECluster CR 本身不携带操作类型信息      │   │
│  │       (操作类型由调用方决定: executeManageDAG vs executeInstallDAG)      │   │
│  │                                                                         │   │
│  │ 方案: 调用方在构建 ExecutionContext 时传入 Operation                      │   │
│  │ • buildTemplateContext 从 BKECluster 提取静态字段 (不变)               │   │
│  │ • 调用方 (executeManageDAG/executeInstallDAG/executeScaleDAG/...)       │   │
│  │   在构建 ExecutionContext 后设置 execCtx.TemplateContext.Operation      │   │
│  │ • shouldExecuteByCondition 读取 execCtx.TemplateContext.Operation       │   │
│  │                                                                         │   │
│  │ 各场景 Operation 值:                                                    │   │
│  │   executeInstallDAG  → Operation = "install"                           │   │
│  │   executeManageDAG   → Operation = "manage"                             │   │
│  │   executeScaleDAG    → Operation = "scale"                              │   │
│  │   executeUpgradeDAG  → Operation = "upgrade"                            │   │
│  │   executeUninstallDAG → Operation = "rollback"                          │   │
│  │                                                                         │   │
│  │ 设计原则:                                                               │   │
│  │ • Operation 是调用上下文信息, 不存储在 BKECluster CR 中               │   │
│  │ • buildTemplateContext 保持只从 BKECluster 提取 (不引入推断逻辑)        │   │
│  │ • 调用方负责设置 Operation (显式传递, 非隐式推断)                       │   │
│  └─────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 代码实现

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

```yaml
# ComponentVersion: manage — 仅纳管场景执行 (KEP-18 Condition)
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: manage-v1.0.0
spec:
  name: manage
  type: inline
  version: "v1.0.0"
  inline:
    handler: EnsureClusterManage
    version: "v1.0.0"
  # ★ 仅纳管场景执行, 全新安装时跳过 (KEP-18 Condition 过滤)
  condition: '{{ eq .Operation "manage" }}'
```

```go
// DeclarativeInstallCatalog 新增
{ Name: "manage", Mode: ExecutionInline, InlineHandler: "EnsureClusterManage", LegacyPhase: "EnsureClusterManage" },
```

```go
// pkg/phaseframe/phases/ensure_cluster_manage.go — 改造为 inline handler

type EnsureClusterManage struct {
    phaseframe.BasePhase
}

func (e *EnsureClusterManage) Execute() (ctrl.Result, error) {
    bkeCluster := e.Ctx.BKECluster
    vc := e.GetVersionContext()

    // 1. 探测已有集群的组件版本
    detectedVersions, err := e.detectClusterVersions(bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 2. 填充 VersionContext.Current (供后续组件 Decide 判断)
    for name, ver := range detectedVersions {
        vc.SetCurrent(name, ver)
    }

    // 3. 写入 BKECluster.Status (供 Legacy 兼容路径读取)
    if v, ok := detectedVersions["kubernetes-master"]; ok {
        bkeCluster.Status.KubernetesVersion = v
    }
    if v, ok := detectedVersions["etcd"]; ok {
        bkeCluster.Status.EtcdVersion = v
    }
    if v, ok := detectedVersions["containerd"]; ok {
        bkeCluster.Status.ContainerdVersion = v
    }

    // 4. 标记纳管完成
    bkeCluster.Status.Managed = true
    e.Ctx.Log.Info("cluster manage completed", "detectedVersions", detectedVersions)

    return ctrl.Result{}, nil
}

// detectClusterVersions 探测已有集群的组件版本
func (e *EnsureClusterManage) detectClusterVersions(
    bkeCluster *bkev1beta1.BKECluster,
) (map[string]string, error) {
    result := make(map[string]string)

    // 获取目标集群客户端
    targetClient, err := e.Ctx.GetTargetK8sClient()
    if err != nil {
        return nil, err
    }

    // 探测 kube-apiserver 版本
    versionInfo, err := targetClient.Discovery().ServerVersion()
    if err == nil {
        result["kubernetes-master"] = versionInfo.GitVersion
        result["kubernetes-worker"] = versionInfo.GitVersion
    }

    // 探测 etcd 版本 (从 etcd Pod image tag)
    etcdVersion, err := detectEtcdVersion(targetClient)
    if err == nil {
        result["etcd"] = etcdVersion
    }

    // 探测 containerd 版本 (从 Node 节点 SSH 查询)
    containerdVersion, err := detectContainerdVersion(bkeCluster)
    if err == nil {
        result["containerd"] = containerdVersion
    }

    // 探测 addon 版本 (从 Deployment/DaemonSet image tag)
    corednsVersion, _ := detectAddonVersion(targetClient, "kube-system", "k8s-app=kube-dns")
    if corednsVersion != "" {
        result["coredns"] = corednsVersion
    }

    kubeProxyVersion, _ := detectAddonVersion(targetClient, "kube-system", "k8s-app=kube-proxy")
    if kubeProxyVersion != "" {
        result["kube-proxy"] = kubeProxyVersion
    }

    return result, nil
}
```

```go
// pkg/upgrade/build_release.go — 纳管专用 VersionContext 构建

// BuildVersionContextForManage 为纳管场景构建 VersionContext
func BuildVersionContextForManage(
    targetBundle *releasemanifest.Bundle,
    detectedVersions map[string]string,
) *VersionContext {
    vc := NewVersionContext()

    // Current 来自探测结果 (已有集群的实际版本)
    for name, ver := range detectedVersions {
        vc.SetCurrent(name, ver)
    }

    // Target 来自 ReleaseImage (期望达到的目标版本)
    if targetBundle.Release.Spec.Install != nil {
        for _, comp := range targetBundle.Release.Spec.Install.Components {
            vc.SetTarget(comp.Name, comp.Version)
        }
    }
    if targetBundle.Release.Spec.Upgrade != nil {
        for _, comp := range targetBundle.Release.Spec.Upgrade.Components {
            vc.SetTarget(comp.Name, comp.Version)
        }
    }

    return vc
}
```

#### 场景判断

纳管场景需要先判断 BKECluster 是否处于纳管模式，再走纳管 DAG 路径：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              纳管场景判断逻辑                                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  reconcileCluster 入口:                                                         │
│                                                                                 │
│    BKECluster.Spec.Manage == true?                                              │
│    ├── true → 纳管场景 ★                                                        │
│    │     → executeManageDAG()                                                    │
│    │     → manage 组件探测版本 → 填充 VC.Current                                 │
│    │     → 后续组件 Decide: Skip(版本一致) / Upgrade(版本不同) / Install(缺失)  │
│    │                                                                            │
│    └── false → 非纳管 → 继续判断其他场景 (安装/升级/扩容/删除/...)             │
│                                                                                 │
│  纳管与全新安装的区别:                                                           │
│    全新安装: 集群不存在，Current 全空 → 全部 DecisionInstall                   │
│    纳管:     集群已运行，Current 从探测获取 → 部分组件可能 DecisionSkip        │
│                                                                                 │
│  纳管的特殊情况:                                                                 │
│    1. 已有集群版本 == 目标版本 → 全部 Skip (无需安装/升级)                       │
│    2. 已有集群版本 < 目标版本 → 部分组件 DecisionUpgrade (纳管 + 升级)        │
│    3. 已有集群缺少某些组件 → 部分组件 DecisionInstall (纳管 + 补装)           │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

```go
// controllers/capbke/bkecluster_controller.go — 纳管场景判断

// isManage 判断是否为纳管场景
func (r *BKEClusterReconciler) isManage(bkeCluster *bkev1beta1.BKECluster) bool {
    // BKECluster.Spec.Manage = true 表示纳管模式
    return bkeCluster.Spec.Manage
}

// reconcileCluster 中纳管场景的分发 (§9.4.7 移除后的执行入口中)
func (r *BKEClusterReconciler) reconcileCluster(ctx, phaseCtx, oldCluster, newCluster) {
    // 暂停检查 (最优先)
    if newCluster.Spec.Pause {
        newCluster.Status.ClusterStatus = bkev1beta1.ClusterPaused
        return
    }

    // 场景判断 (无 Legacy 回退)
    switch {
    case isDeleteOrReset(newCluster):
        r.executeUninstallDAG(ctx, phaseCtx, oldCluster, newCluster)

    case isManage(newCluster):   // ★ 纳管场景判断
        r.executeManageDAG(ctx, phaseCtx, oldCluster, newCluster)

    case isDryRun(newCluster):
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster, WithDryRun())

    case isScale(oldCluster, newCluster):
        r.executeScaleDAG(ctx, phaseCtx, oldCluster, newCluster)

    case r.shouldUseDeclarativeUpgrade(newCluster):
        r.executeUpgradeDAG(ctx, phaseCtx, oldCluster, newCluster)

    default:
        // 全新安装
        r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    }
}
```

> **注意**：纳管场景判断 (`isManage`) 在扩容 (`isScale`) 和升级 (`shouldUseDeclarativeUpgrade`) 之前，因为纳管模式优先于其他操作 — 纳管时先探测版本，再根据 VersionContext 决定后续操作 (安装/升级/跳过)。

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
    // Target 来自 ReleaseImage (包含 manage 组件, 与全新安装相同)
    // ★ manage 组件的执行/跳过由 ComponentVersion.Spec.Condition 控制 (KEP-18):
    //   Condition: '{{ eq .Operation "manage" }}'
    //   纳管场景 Operation="manage" → 求值 true → 执行 manage 探测
    //   全新安装 Operation="install" → 求值 false → 跳过 manage
    upgrade.FillTargetFromBundle(vc, bundle)
    phaseCtx.SetVersionContext(vc)

    // 3. 构建 DAG (包含所有 install.components，包括 manage)
    //    ★ 与 executeInstallDAG 使用相同的构建函数 (无 excludeComponents 参数)
    //    ★ manage 的执行/跳过由 Condition 在 shouldExecuteByCondition 中判断 (非 VersionContext 构建 时过滤)
    dag, err := upgrade.BuildInstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    // manage 组件在第一个 Batch 执行: 探测版本 → 填充 VC.Current → 后续组件 Decide

    // 4. 构建 Scheduler (manage handler 需优先注册)
    factory := componentfactory.NewFactoryFromBundle(bundle)
    factory.RegisterInstallHandlers() // 含 EnsureClusterManage

    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:       NewInlinePhaseRunnerAdapter(phaseCtx, &PhaseRunner{Factory: factory}),
        ManifestStore:      manifest.NewBundleStore(bundle),
        CVStore:            manifest.NewBundleStore(bundle),
        MaxParallelPerBatch: 1, // ★ 纳管首个 Batch 串行 (manage 先执行，填充 Current 后后续组件才能正确 Decide)
    })

    execCtx := buildExecutionContext(ctx, r.Client, newCluster, vc)

    // ★ 设置 Operation 到 TemplateContext (供 Condition 求值)
    // buildTemplateContext 从 BKECluster 提取静态字段, 不含操作类型
    // Operation 是调用上下文信息, 由调用方显式设置
    execCtx.TemplateContext.Operation = "manage"

    // 5. 执行 DAG — shouldExecuteByCondition 在 Scheduler 跳过链中生效
    //    Scheduler 跳过链 (executeBatchParallel, scheduler.go:160-183):
    //      (A) shouldSkipComponent(node)         → DeclarativeUpgradeStatus.IsCompleted → Skip
    //      (B) componentNeedsUpgrade(node)       → VersionContext.NeedsExecution → Current==Target → Skip
    //      (C) shouldExecuteByCondition(node)    → EvaluateCondition(cv.Spec.Condition, tmpl) ★ KEP-18
    //      (D) executeComponent(node)            → 分发到 Inline/YAML/Helm Executor
    //
    //    纳管场景执行流程:
    //    Batch 1: [manage]
    //      (A) IsCompleted → false (首次执行)
    //      (B) NeedsExecution → true (Current=="", Target!="")
    //      (C) Condition: '{{ eq .Operation "manage" }}'
    //          → Operation="manage" (由 executeManageDAG 设置)
    //          → 求值 "true" → 执行 ★
    //      (D) InlineComponentExecutor → EnsureClusterManage.Execute()
    //          → 探测版本 → 填充 VC.Current
    //          → 后续组件 Current 有值, 可正确 Decide
    //
    //    Batch 2+: [bkeagent, containerd, kubernetes-master, ...]
    //      (A) IsCompleted → false
    //      (B) NeedsExecution → Current==Target→Skip / Current!=Target→Upgrade / Current==""→Install
    //      (C) Condition: 各组件的 Condition (通常为空, 不过滤)
    //      (D) InlineComponentExecutor → 各 handler Execute()
    //
    //    全新安装场景 (executeInstallDAG, Operation="install"):
    //    Batch 1: [manage]
    //      (C) Condition: '{{ eq .Operation "manage" }}'
    //          → Operation="install" → 求值 "false" → Skip ★ (manage 跳过)
    //          → Current 保持空 → 后续组件全部 DecisionInstall
    //
    //    ★ MaxParallelPerBatch=1 确保 Batch 1 串行: manage 先执行填充 Current,
    //      后续组件才能正确 Decide (Current 有值)
    return sched.ExecuteDAG(ctx, execCtx, dag)
}
```

### 10.3 集群扩容 DAG 化

#### 设计思路

扩容是指向已有集群新增 Master 或 Worker 节点。与全新安装的核心区别在于：已有节点不需要重新安装，仅新节点需要执行安装操作。

扩容的节点安装触发采用**三层机制**：第一层是 DAG Scheduler 的组件级 Decision（VersionContext + Condition），决定组件是否执行；第二层是 PhaseRunner 的 `NeedExecute()` 前置守卫（StateCode），避免不必要的 Execute 调用；第三层是 inline handler 内部的节点级过滤（StateCode 位标记），决定哪些节点需要操作。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              扩容 DAG 化设计思路                                                   │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    PhaseFlow.CalculatePhase() 遍历完整 DeployPhases (11 个 Phase):              │
│      EnsureBKEAgent → EnsureNodesEnv → EnsureCerts → EnsureLoadBalance          │
│      → EnsureMasterInit → EnsureMasterJoin → EnsureWorkerJoin → ...            │
│                                                                                 │
│    每个 Phase 的 NeedExecute() 检查 BKENode.Status.StateCode 位标记:             │
│      新增节点 (StateCode=0): 位标记未设置 → NeedExecute=true → 执行             │
│      已有节点 (位标记已设置): NeedExecute=false → 跳过                           │
│                                                                                 │
│    ★ 不存在独立的 ScalePhases 列表用于执行                                       │
│    ★ ClusterScaleMasterUpPhaseNames 等仅用于状态上报 (设置 ClusterStatus)       │
│    ★ 节点安装 (bkeagent/containerd) 和节点加入 (kubeadm join) 由同一个          │
│       PhaseFlow 驱动, 按 DeployPhases 拓扑顺序依次执行                           │
│                                                                                 │
│    问题: 扩容复用 DeployPhases, 但无法复用 DAG 的版本决策和断点续传              │
│          PhaseFlow 串行执行, 无法实现组件间并行                                  │
│                                                                                 │
│  DAG 化方案 (三层机制):                                                          │
│                                                                                 │
│  第一层: Scheduler 组件级 Decision (VersionContext + Condition)                  │
│    Scheduler 跳过链 (executeBatchParallel):                                      │
│      (A) shouldSkipComponent → DeclarativeUpgradeStatus.IsCompleted → Skip      │
│      (B) componentNeedsUpgrade → VersionContext.NeedsExecution:                 │
│          集群级组件 (certs, coredns, ...): Current==Target → Skip (已安装)       │
│          节点级组件 (bkeagent, containerd, ...): Current=="" → Install (执行)   │
│      (C) shouldExecuteByCondition → EvaluateCondition(cv.Spec.Condition, tmpl): │
│          kubernetes-master: condition '{{ eq .ScaleType "master" }}'            │
│            Master 扩容 → ScaleType="master" → 求值 "true" → 执行 ★              │
│            Worker 扩容 → ScaleType="worker" → 求值 "false" → Skip ★             │
│          kubernetes-worker: condition '{{ eq .ScaleType "worker" }}'            │
│            Worker 扩容 → ScaleType="worker" → 求值 "true" → 执行 ★              │
│            Master 扩容 → ScaleType="master" → 求值 "false" → Skip ★             │
│          无 Condition 的组件 (bkeagent, containerd, ...): 跳过此层, 执行        │
│      (D) executeComponent → 分发到 Inline/YAML/Helm Executor                    │
│    ★ VersionContext 是组件级的, Condition 也是组件级的                          │
│    ★ 这一层决定"组件是否执行", 不决定"哪些节点执行"                               │
│                                                                                 │
│  第二层: PhaseRunner.NeedExecute() 前置守卫 (StateCode)                          │
│    PhaseRunner.Execute() 在调用 phase.Execute() 前先调用 phase.NeedExecute():   │
│      NeedExecute=true  → 有节点位标记未设置 → 继续 Execute()                     │
│      NeedExecute=false → 所有节点位标记已设置 → return nil (跳过 Execute)         │
│    ★ 这是 Execute() 的前置守卫, 与第三层检查相同的 StateCode, 但目的不同:       │
│      NeedExecute: 布尔值判断 (是否有节点需要操作), 避免不必要的 Execute 调用     │
│      Execute 内部: 精确节点列表 (哪些节点需要操作), 返回具体节点执行操作          │
│                                                                                 │
│  第三层: inline handler 节点级过滤 (StateCode 位标记)                             │
│    handler 的 Execute() 内部通过 StateCode 位标记精确过滤目标节点:              │
│      已有节点: 对应位标记已设置 (NodeEnvFlag/NodeBootFlag/MasterInitFlag)       │
│                → 跳过                                                            │
│      新增节点: 对应位标记未设置                                                   │
│                → 执行安装                                                        │
│    ★ 过滤方式因 Phase 而异 (见下方 "节点过滤方式对比" 表)                        │
│    ★ 这一层决定"哪些节点执行"                                                    │
│                                                                                 │
│  核心设计:                                                                       │
│  1. EnsureMasterInit handler 幂等改造 — 区分 init (首个 Master) / join (后续)  │
│     通过 getExistingMasterNodes 检查已有 Master 数量判断执行 init 还是 join      │
│  2. 三层过滤 — VersionContext+Condition + NeedExecute + filterNodes 逐层收窄:   │
│     第一层: VersionContext Current=="" + Condition ScaleType → 组件执行/跳过     │
│     第二层: NeedExecute(StateCode) → 有节点需要操作 (避免空执行)                  │
│     第三层: filterNodes(StateCode) → 精确定位目标节点                              │
│  3. 复用安装 DAG — 无需独立的扩容 DAG，安装 DAG + 组件级 Current/Condition 过滤   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### Legacy PhaseFlow 扩容执行机制（当前代码）

**核心代码路径**: `pkg/phaseframe/phases/phase_flow.go` + `pkg/phaseframe/phases/list.go`

**1. ClusterScaleXxxPhaseNames 仅用于状态上报，不用于执行决策**

代码中存在 4 个扩缩容相关的 Phase 名称列表（`list.go:116-128`），它们**仅用于状态上报**（`calculateClusterStatusByPhase` 根据当前执行的 Phase 名称设置对应的 `ClusterStatus`），**不用于决定执行哪些 Phase**：

```go
// pkg/phaseframe/phases/list.go:116-128 — 仅用于状态上报, 不用于执行

ClusterScaleMasterUpPhaseNames = []confv1beta1.BKEClusterPhase{
    EnsureMasterJoinName,       // Master 扩容
}
ClusterScaleWorkerUpPhaseNames = []confv1beta1.BKEClusterPhase{
    EnsureWorkerJoinName,       // Worker 扩容
}
ClusterScaleMasterDownPhaseNames = []confv1beta1.BKEClusterPhase{
    EnsureMasterDeleteName,     // Master 缩容
}
ClusterScaleWorkerDownPhaseNames = []confv1beta1.BKEClusterPhase{
    EnsureWorkerDeleteName,     // Worker 缩容
}
```

**状态上报的使用方式**（`phase_flow.go:371-405`）：

```go
// calculateClusterStatusByPhase 在 Phase 执行后/后置 Hook 中被调用
// 根据当前 Phase 名称匹配上述列表，设置对应的 ClusterStatus

func calculateClusterStatusByPhase(phase phaseframe.Phase, err error) error {
    phaseName := phase.Name()
    switch {
    // ...
    case phaseName.In(ClusterScaleMasterUpPhaseNames):
        handleClusterScaleMasterUpPhase(ctx, err)   // → ClusterMasterScalingUp / ClusterScaleFailed
    case phaseName.In(ClusterScaleWorkerUpPhaseNames):
        handleClusterScaleWorkerUpPhase(ctx, err)   // → ClusterWorkerScalingUp / ClusterScaleFailed
    case phaseName.In(ClusterScaleMasterDownPhaseNames):
        handleClusterScaleMasterDownPhase(ctx, err) // → ClusterMasterScalingDown / ClusterScaleFailed
    case phaseName.In(ClusterScaleWorkerDownPhaseNames):
        handleClusterScaleWorkerDownPhase(ctx, err) // → ClusterWorkerScalingDown / ClusterScaleFailed
    // ...
    }
}

// handleClusterScaleMasterUpPhase — 设置 ClusterStatus
func handleClusterScaleMasterUpPhase(ctx *phaseframe.PhaseContext, err error) {
    if err != nil {
        ctx.BKECluster.Status.ClusterStatus = bkev1beta1.ClusterScaleFailed
    } else {
        ctx.BKECluster.Status.ClusterStatus = bkev1beta1.ClusterMasterScalingUp
    }
}
```

**执行决策与状态上报的分离**：

| 职责 | 机制 | 代码位置 | 说明 |
|------|------|---------|------|
| **执行哪些 Phase** | `DeployPhases` + `NeedExecute()` | `phase_flow.go:78` `calculateAndAddPhases` | 遍历全部 11 个 DeployPhases，每个 Phase 自行判断是否需要执行 |
| **设置什么 ClusterStatus** | `ClusterScaleXxxPhaseNames` | `phase_flow.go:380` `calculateClusterStatusByPhase` | 根据当前 Phase 名称匹配列表，设置扩缩容状态 |

**为什么不用独立 ScalePhases 列表驱动执行**：

1. **节点安装需要复用 DeployPhases**：扩容时新节点需要 bkeagent 推送、containerd 安装、环境初始化等，这些与全新安装完全相同，复用 DeployPhases 避免代码重复
2. **已有节点自动跳过**：DeployPhases 中每个 Phase 的 `NeedExecute()` 基于 `StateCode` 位标记判断，已有节点位标记已设置 → 跳过，新节点位标记未设置 → 执行
3. **状态上报仅需要 Phase 名称**：`ClusterScaleMasterUpPhaseNames` 只需包含 `EnsureMasterJoinName`，因为只有执行到 Master 加入时才需要上报 `ClusterMasterScalingUp` 状态；bkeagent/containerd 等安装阶段不需要特殊状态上报（默认 `ClusterInitializing` 或 `ClusterUnknown`）

**状态上报的完整链路**：

```
扩容执行流程:

  PhaseFlow.Execute()
    → 遍历 BKEPhases (由 NeedExecute 筛选)
      → EnsureBKEAgent.Execute()
        → ExecutePostHook → calculateClusterStatusByPhase
          → phaseName 不在 ScaleXxx 列表中
          → ClusterStatus = ClusterInitializing (默认)
      → EnsureNodesEnv.Execute()
        → ExecutePostHook → calculateClusterStatusByPhase
          → phaseName 不在 ScaleXxx 列表中
          → ClusterStatus = ClusterInitializing (默认)
      → EnsureMasterJoin.Execute()      ← Master 扩容时
        → ExecutePostHook → calculateClusterStatusByPhase
          → phaseName.In(ClusterScaleMasterUpPhaseNames) ✓
          → handleClusterScaleMasterUpPhase(ctx, err)
          → ClusterStatus = ClusterMasterScalingUp ★ (扩容专属状态)
      → EnsureWorkerJoin.Execute()      ← Worker 扩容时
        → ExecutePostHook → calculateClusterStatusByPhase
          → phaseName.In(ClusterScaleWorkerUpPhaseNames) ✓
          → handleClusterScaleWorkerUpPhase(ctx, err)
          → ClusterStatus = ClusterWorkerScalingUp ★ (扩容专属状态)
```

**结论**：`ClusterScaleXxxPhaseNames` 是状态上报的"标签"——当 PhaseFlow 执行到特定 Phase（如 `EnsureMasterJoin`）时，通过名称匹配设置对应的扩缩容 `ClusterStatus`。它不参与执行决策，执行决策由 `DeployPhases` + `NeedExecute()` 完成。

**2. PhaseFlow 遍历完整 DeployPhases**

扩容时 `PhaseFlow.CalculatePhase()` 遍历 `FullPhasesRegisFunc`（`CommonPhases + DeployPhases + PostDeployPhases`），每个 Phase 的 `NeedExecute()` 自行判断是否需要执行：

```go
// pkg/phaseframe/phases/phase_flow.go:78 — 遍历全部 Phase, 非仅 Scale 相关
func (p *PhaseFlow) calculateAndAddPhases(old, new *bkev1beta1.BKECluster, phasesFuncs ...) {
    for _, f := range phasesFuncs {
        phase := f(p.ctx)
        if phase.NeedExecute(old, new) {  // ★ 每个 Phase 自行判断
            p.BKEPhases = append(p.BKEPhases, phase)
        }
    }
}
```

`DeployPhases` 包含全部 11 个安装 Phase（`list.go:32-44`）：

```go
DeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureBKEAgent,        // bkeagent 推送
    NewEnsureNodesEnv,         // containerd + 系统配置
    NewEnsureClusterAPIObj,    // ClusterAPI 对象
    NewEnsureCerts,            // 证书
    NewEnsureLoadBalance,      // 负载均衡
    NewEnsureMasterInit,       // Master 初始化
    NewEnsureMasterJoin,       // Master 加入
    NewEnsureWorkerJoin,       // Worker 加入
    NewEnsureAddonDeploy,      // 组件部署
    NewEnsureNodesPostProcess, // 后置处理
    NewEnsureAgentSwitch,      // Agent 切换
}
```

**3. 每个 Phase 的 NeedExecute 判断**

每个 Phase 通过 `phaseutil.HasNodesNeedingPhase()` 或 `phaseutil.filterNodes()` 检查 `BKENode.Status.StateCode` 位标记，判断是否有节点需要操作：

| Phase | NeedExecute 判断逻辑 | 新节点 (StateCode=0) | 已有节点 (位标记已设置) |
|-------|---------------------|---------------------|----------------------|
| `EnsureBKEAgent` | `HasNodesNeedingPhase(bkeNodes, NodeAgentPushedFlag)` | **true** → 推送 bkeagent | false → 跳过 |
| `EnsureNodesEnv` | `HasNodesNeedingPhase(bkeNodes, NodeEnvFlag)` | **true** → 安装 containerd/系统配置 | false → 跳过 |
| `EnsureCerts` | 证书已存在 → false | false → 跳过 | false → 跳过 |
| `EnsureClusterAPIObj` | CAPI 对象已存在 → false | false → 跳过 | false → 跳过 |
| `EnsureLoadBalance` | LB 已配置 → false | false → 跳过 | false → 跳过 |
| `EnsureMasterInit` | 已有 Master 且无新 Master → false | **true** → init/join | false → 跳过 |
| `EnsureMasterJoin` | `GetNeedJoinMasterNodesWithBKENodes()` | **true** → kubeadm join | false → 跳过 |
| `EnsureWorkerJoin` | `GetNeedJoinWorkerNodesWithBKENodes()` | **true** → kubeadm join | false → 跳过 |
| `EnsureAddonDeploy` | Addon 已部署 → false | false → 跳过 | false → 跳过 |
| `EnsureNodesPostProcess` | `HasNodesNeedingPhase(NodePostProcessFlag)` | **true** → 后置处理 | false → 跳过 |
| `EnsureAgentSwitch` | 已切换 → false | false → 跳过 | false → 跳过 |

`HasNodesNeedingPhase` 实现（`phaseutil/util.go:1273`）：

```go
func HasNodesNeedingPhase(bkeNodes bkev1beta1.BKENodes, flag int) bool {
    for _, bn := range bkeNodes {
        if bn.Status.StateCode&flag == 0 {  // 位标记未设置
            // 排除 Failed/Deleting/NeedSkip 节点
            if bn.Status.StateCode&bkev1beta1.NodeFailedFlag == 0 &&
                bn.Status.StateCode&bkev1beta1.NodeDeletingFlag == 0 &&
                !bn.Status.NeedSkip {
                return true  // 有节点需要执行此 Phase
            }
        }
    }
    return false
}
```

**4. Phase 内部节点过滤（两种模式）**

Phase 执行时通过 StateCode 位标记精确过滤目标节点。代码中存在两种过滤模式：

| 模式 | 使用 Phase | 过滤方式 | 代码位置 |
|------|-----------|---------|---------|
| **`filterNodes()` 模式** | EnsureBKEAgent, EnsureMasterJoin, EnsureWorkerJoin, EnsureNodesPostProcess | 调用 `phaseutil.GetNeedXxxNodesWithBKENodes()` → 内部调用 `filterNodes()` + `NodePredicate` | `phaseutil/util.go:181` |
| **内联循环模式** | EnsureNodesEnv | `Execute()` 内部直接遍历 BKENodes，逐个检查 StateCode 位标记 | `ensure_nodes_env.go:101` `getNodesToInitEnv()` |

**`filterNodes()` 模式示例**（`ensure_bke_agent.go:165`）：

```go
// EnsureBKEAgent.Execute() → getNeedPushNodes()
func (e *EnsureBKEAgent) getNeedPushNodes() error {
    bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, e.Ctx.BKECluster)
    // ...
    // ★ 调用 phaseutil.GetNeedPushAgentNodesWithBKENodes()
    //   → 内部调用 filterNodes(cluster, predicate, WithExcludeAppointmentNodes(), WithBKENodes(bkeNodes))
    //   → predicate = !NodeAgentPushedFlag
    nodes := phaseutil.GetNeedPushAgentNodesWithBKENodes(e.Ctx.BKECluster, bkeNodes)
    e.needPushNodes = nodes
    return nil
}
```

**内联循环模式示例**（`ensure_nodes_env.go:101`）：

```go
// EnsureNodesEnv.Execute() → getNodesToInitEnv()
// ★ 不调用 filterNodes(), 直接遍历 BKENodes 检查 StateCode
func (e *EnsureNodesEnv) getNodesToInitEnv() bkenode.Nodes {
    for _, bn := range bkeNodes {
        // 硬排除: Failed/Deleting/NeedSkip
        if bn.Status.StateCode&bkev1beta1.NodeFailedFlag != 0 ||
           bn.Status.StateCode&bkev1beta1.NodeDeletingFlag != 0 ||
           bn.Status.NeedSkip { continue }
        // 已完成: NodeEnvFlag 已设置 → 跳过
        if bn.Status.StateCode&bkev1beta1.NodeEnvFlag != 0 { continue }
        // 前置未就绪: agent 未就绪 → 跳过
        if bn.Status.StateCode&bkev1beta1.NodeAgentReadyFlag == 0 { continue }
        exceptEnvNodes = append(exceptEnvNodes, bn.ToNode())  // ★ 需要操作的节点
    }
    return exceptEnvNodes
}
```

> **两种模式的差异**：`filterNodes()` 模式通过 `NodePredicate` 函数封装过滤逻辑，可复用 `WithExcludeAppointmentNodes` 等 option；内联循环模式直接在 Phase 内遍历，可检查多个前置条件（如 `NodeAgentReadyFlag` + `NodeEnvFlag` 组合判断）。两种模式最终效果一致——基于 StateCode 位标记过滤目标节点。

**节点级 StateCode 位标记说明**：

| 位标记 | 含义 | 设置时机 | 过滤用途 |
|--------|------|---------|---------|
| `NodeAgentPushedFlag` | bkeagent 已推送 | bkeagent 安装完成 | `GetNeedPushAgentNodes`: 未设置 → 需推送 |
| `NodeAgentReadyFlag` | bkeagent 已就绪 | bkeagent 注册完成 | `ensure_nodes_env`: 未设置 → 等待 agent 就绪 |
| `NodeEnvFlag` | 节点环境已初始化 | containerd/系统配置安装完成 | `GetNeedInitEnvNodes`: 未设置 → 需初始化环境 |
| `NodeBootFlag` | 节点已加入集群 | kubeadm join/init 完成 | `GetNeedJoinNodes`: 未设置 → 需加入 |
| `MasterInitFlag` | Master 已初始化 | kubeadm init 完成 | `GetNeedJoinNodes`: 未设置 → 需初始化 |
| `NodeFailedFlag` | 节点失败 | 安装失败 | `filterNodes`: 已设置 → 硬排除 |
| `NodeDeletingFlag` | 节点删除中 | 缩容触发 | `filterNodes`: 已设置 → 硬排除 |

#### DAG 化方案设计

**1. 入口集成 — 复用 executePhaseFlow 模式**

扩容 DAG 化复用升级 DAG 的入口集成模式：在 `executePhaseFlow()` 中增加 `shouldUseScaleDAG()` 判断，类似 `shouldUseDeclarativeUpgrade()`：

```go
// controllers/capbke/bkecluster_controller.go — executePhaseFlow 扩容 DAG 集成

func (r *BKEClusterReconciler) executePhaseFlow(...) (ctrl.Result, error) {
    phaseCtx := phaseframe.NewReconcilePhaseCtx(ctx)... 

    // ★ 扩容 DAG 路径 (新增)
    if r.shouldUseScaleDAG(bkeCluster, oldBkeCluster) {
        bkeClusterLogger().Infof("running scale DAG")
        dagCompleted, dagResult, dagErr := r.executeScaleDAG(ctx, phaseCtx, oldBkeCluster, bkeCluster, bkeLogger)
        if dagErr != nil { return dagResult, dagErr }
        if dagResult.Requeue || dagResult.RequeueAfter > 0 { return dagResult, nil }
        if !dagCompleted { return ctrl.Result{}, nil }
        // 扩容 DAG 完成后不再走 PhaseFlow
        // ★ 扩容 DAG 复用安装 DAG, 处理全部组件 (inline + yaml/helm), 无需 PhaseFlow 补充
        //   升级 DAG 同样处理全部组件 (inline + yaml/helm), DAG 完成后 PhaseFlow 中
        //   升级相关 Phase 的 NeedExecute=false 自动跳过 (skipPhaseAfterDeclarativeDAG)
        return ctrl.Result{}, nil
    }

    // 升级 DAG 路径 (现有)
    if r.shouldUseDeclarativeUpgrade(bkeCluster) { ... }

    // PhaseFlow 路径 (现有)
    flow := phases.NewPhaseFlow(phaseCtx) ...
}

// shouldUseScaleDAG 判断是否走扩容 DAG 路径
func (r *BKEClusterReconciler) shouldUseScaleDAG(
    bkeCluster *bkev1beta1.BKECluster,
    oldCluster *bkev1beta1.BKECluster,
) bool {
    // 1. Feature Gate 启用
    if !featuregate.DeclarativeScaleEnabled(bkeCluster) { return false }
    // 2. 扩容场景 (节点数增加)
    if !r.isScale(oldCluster, bkeCluster) { return false }
    // 3. ReleaseImage 已就绪 (install-ready 或 upgrade-ready annotation 存在)
    if _, ok := featuregate.InstallReady(bkeCluster); !ok { return false }
    return true
}
```

**2. 安装 handler 注册**

扩容 DAG 复用安装 DAG 的组件拓扑，需要注册安装 handler（当前代码仅注册了升级 handler）：

```go
// pkg/componentfactory/registry.go — 扩展 registerInlineHandler 支持安装 handler

func registerInlineHandler(f *ComponentFactory, handler, version string) error {
    switch handler {
    // 升级 handler (现有)
    case upgrade.InlineHandlerEtcdUpgrade:
        f.Register(handler, version, phases.NewEnsureEtcdUpgrade)
    case upgrade.InlineHandlerMasterUpgrade:
        f.Register(handler, version, phases.NewEnsureMasterUpgrade)
    // ... 其他升级 handler ...

    // 安装 handler (新增, 扩容 DAG 复用安装 handler)
    case "EnsureBKEAgent":
        f.Register(handler, version, phases.NewEnsureBKEAgent)
    case "EnsureNodesEnv":
        f.Register(handler, version, phases.NewEnsureNodesEnv)
    case "EnsureCerts":
        f.Register(handler, version, phases.NewEnsureCerts)
    case "EnsureClusterAPIObj":
        f.Register(handler, version, phases.NewEnsureClusterAPIObj)
    case "EnsureLoadBalance":
        f.Register(handler, version, phases.NewEnsureLoadBalance)
    case "EnsureMasterInit":
        f.Register(handler, version, phases.NewEnsureMasterInit)
    case "EnsureWorkerJoin":
        f.Register(handler, version, phases.NewEnsureWorkerJoin)
    case "EnsureNodesPostProcess":
        f.Register(handler, version, phases.NewEnsureNodesPostProcess)
    case "EnsureAgentSwitch":
        f.Register(handler, version, phases.NewEnsureAgentSwitch)
    default:
        return fmt.Errorf("unknown inline handler %q", handler)
    }
    return nil
}
```

**3. 三层执行机制 — 代码调用链**

```
executeScaleDAG
  → Scheduler.ExecuteDAG
    → shouldSkipComponent(node)         ← (A) DeclarativeUpgradeStatus.IsCompleted → Skip
    → componentNeedsUpgrade(node)       ← (B) VersionContext.NeedsExecution:
                                            集群级 Current==Target → Skip
                                            节点级 Current=="" → NeedsExecution=true
    → shouldExecuteByCondition(node)    ← (C) EvaluateCondition(cv.Spec.Condition, tmpl) ★ KEP-18
                                            kubernetes-master: '{{ eq .ScaleType "master" }}'
                                              Master 扩容 → "true" → 执行
                                              Worker 扩容 → "false" → Skip
                                            无 Condition 的组件 → 跳过此层, 执行
    → executeComponent(node)            ← (D) 分发到 Inline/YAML/Helm Executor
      → InlineComponentExecutor.ExecuteComponent
        → NeedsExecution(vc, node.Name) ← (B) 再次检查 (inline_executor.go:55)
        → Runner.Execute(handler, version)
          → PhaseRunner.Execute           ← runner.go:28
            → phase.NeedExecute(old, new) ← 第二层 (前置守卫): StateCode 检查, 无节点需要则 return nil
            → phase.Execute()             ← 第三层 (精确过滤): StateCode 位标记过滤目标节点
```

**PhaseRunner.Execute 的关键逻辑**（现有代码 `runner.go:28-57`，无需修改）：

```go
// pkg/componentfactory/runner.go — 现有代码, DAG 路径和 PhaseFlow 路径共用

func (r *PhaseRunner) Execute(
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    handler, version string,
) error {
    phase, err := ResolveInlineUpgrade(r.Factory, handler, version, phaseCtx)
    if err != nil { return err }
    if !phase.NeedExecute(oldCluster, newCluster) {  // ★ 第二层: StateCode 检查
        return nil  // 无节点需要操作 → 跳过 Execute
    }
    if err := phase.ExecutePreHook(); err != nil { return err }
    result, err := phase.Execute()  // ★ 第三层: 内部 filterNodes 精确过滤
    if postErr := phase.ExecutePostHook(err); postErr != nil { return postErr }
    if err != nil { return err }
    return nil
}
```

**4. 状态上报**

扩容 DAG 路径需要在执行前后设置 `ClusterStatus`（替代 Legacy PhaseFlow 的 `calculateClusterStatusByPhase`）：

```go
// executeScaleDAG 中状态上报 (复用 executeUpgradeDAG 的 patchClusterStatus 模式)

// 执行前: 设置扩容状态
if err := r.patchClusterStatus(newCluster, bkev1beta1.ClusterMasterScalingUp); err != nil {
    return false, ctrl.Result{}, err
}
// 执行 DAG ...
// 执行成功后: 设置 Ready 状态
newCluster.Status.ClusterStatus = bkev1beta1.ClusterStatusReady
```

#### 代码实现

```go
// controllers/capbke/bkecluster_scale_dag.go 🆕新增

// executeScaleDAG 运行扩容 DAG (复用安装 DAG 拓扑)
// 三层机制: VersionContext (组件级) → NeedExecute (节点级) → filterNodes (精确节点)
func (r *BKEClusterReconciler) executeScaleDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
    bkeLogger *bkev1beta1.BKELogger,
) (bool, ctrl.Result, error) {
    // 1. 解析当前集群版本对应的 ReleaseImage bundle
    //    ★ 扩容使用当前版本 (非 DesiredVersion), 新节点安装到当前版本
    currentVersion, err := r.clusterCurrentOpenFuyaoVersion(ctx, newCluster)
    if err != nil || currentVersion == "" {
        return false, ctrl.Result{}, fmt.Errorf("cannot resolve current version for scale: %w", err)
    }
    bundle, _, err := r.resolveUpgradeBundle(ctx, newCluster, currentVersion)
    if err != nil {
        if isReleaseImageNotReady(err) {
            return false, ctrl.Result{RequeueAfter: releaseImageRequeueInterval}, nil
        }
        return false, ctrl.Result{}, err
    }

    // 2. 构建安装 VersionContext (Current 从当前版本 ReleaseImage 填充)
    vc := upgrade.BuildVersionContextForInstall(bundle)
    // ★ 填充集群级组件 Current (跳过已安装), 节点级组件保持为空 (执行安装)
    r.fillCurrentFromExistingNodes(ctx, newCluster, vc)
    phaseCtx.SetVersionContext(vc)

    // 3. 构建安装 DAG (复用安装 DAG, 与全新安装相同的拓扑结构)
    dag, err := upgrade.BuildInstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))
    if err != nil {
        return false, ctrl.Result{}, fmt.Errorf("build scale DAG: %w", err)
    }

    bkeLogger.Info("scale DAG",
        "currentVersion=%s components=%d source=%s",
        currentVersion, len(dag.NodeNames()), bundle.Source,
    )

    // 4. 状态上报: 设置扩容状态
    if err := r.patchClusterStatus(newCluster, bkev1beta1.ClusterMasterScalingUp); err != nil {
        return false, ctrl.Result{}, err
    }

    // 5. 构建 ComponentFactory (注册安装 handler)
    factory, err := componentfactory.NewFactoryFromBundle(bundle)
    if err != nil {
        return false, ctrl.Result{}, fmt.Errorf("build component factory: %w", err)
    }
    factory.RegisterInstallHandlers() // ★ 注册安装 handler (EnsureBKEAgent 等)

    // 6. 构建 Scheduler (复用升级框架)
    bundleStore := manifest.NewBundleStore(bundle)
    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:    NewInlinePhaseRunnerAdapter(phaseCtx, &componentfactory.PhaseRunner{Factory: factory}),
        ManifestStore:   bundleStore,
        CVStore:         bundleStore,
        MaxParallelPerBatch: 8,
    })

    // 7. 构建 ExecutionContext
    execCtx := buildExecutionContext(phaseCtx, oldCluster, newCluster, bkeLogger, nil)

    // ★ 设置 Operation 和 ScaleType 到 TemplateContext (供 Condition 求值)
    // buildTemplateContext 从 BKECluster 提取静态字段, 不含操作类型
    // Operation/ScaleType 是调用上下文信息, 由调用方显式设置 (KEP-18 §3.3)
    execCtx.TemplateContext.Operation = "scale"
    execCtx.TemplateContext.ScaleType = inferScaleType(oldCluster, newCluster) // "master" / "worker"

    // 8. 执行 DAG — shouldExecuteByCondition 在 Scheduler 跳过链 (C) 层生效
    //    Scheduler 跳过链 (executeBatchParallel, scheduler.go:160-183):
    //      (A) shouldSkipComponent(node)         → DeclarativeUpgradeStatus.IsCompleted → Skip
    //      (B) componentNeedsUpgrade(node)       → VersionContext.NeedsExecution:
    //                                                集群级 Current==Target → Skip (certs, coredns 已安装)
    //                                                节点级 Current=="" → NeedsExecution=true (bkeagent, containerd)
    //      (C) shouldExecuteByCondition(node)    → EvaluateCondition(cv.Spec.Condition, tmpl) ★ KEP-18:
    //                                                kubernetes-master: '{{ eq .ScaleType "master" }}'
    //                                                  Master 扩容 → ScaleType="master" → "true" → 执行 ★
    //                                                  Worker 扩容 → ScaleType="worker" → "false" → Skip ★
    //                                                kubernetes-worker: '{{ eq .ScaleType "worker" }}'
    //                                                  Worker 扩容 → ScaleType="worker" → "true" → 执行 ★
    //                                                  Master 扩容 → ScaleType="master" → "false" → Skip ★
    //                                                无 Condition 的组件 (bkeagent, containerd): 跳过此层, 执行
    //      (D) executeComponent(node)            → 分发到 Inline/YAML/Helm Executor
    //    第二层 (PhaseRunner.NeedExecute): StateCode 前置守卫, 无节点需要则 return nil
    //    第三层 (handler Execute): StateCode 位标记过滤目标节点 (已有节点跳过, 新增节点执行)
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        return false, ctrl.Result{}, fmt.Errorf("execute scale DAG: %w", err)
    }

    // 9. 扩容完成: 更新状态
    newCluster.Status.ClusterStatus = bkev1beta1.ClusterStatusReady
    return true, ctrl.Result{}, nil
}

// fillCurrentFromExistingNodes 从当前版本 ReleaseImage 填充 VersionContext.Current
// 仅填充集群级组件的 Current → DecisionSkip (跳过已安装的集群级组件)
// 节点级组件 Current 保持为空 → DecisionInstall → PhaseRunner.NeedExecute + filterNodes 过滤
func (r *BKEClusterReconciler) fillCurrentFromExistingNodes(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    vc *upgrade.VersionContext,
) {
    // 1. 解析当前集群版本对应的 ReleaseImage bundle
    currentVersion, err := r.clusterCurrentOpenFuyaoVersion(ctx, bkeCluster)
    if err != nil || currentVersion == "" {
        return // 无法解析当前版本, Current 全空 → 所有组件 DecisionInstall
    }
    currentBundle, err := r.resolveCurrentReleaseBundle(ctx, bkeCluster, currentVersion)
    if err != nil || currentBundle == nil {
        return // 当前版本 ReleaseImage 不可用, Current 全空 → 所有组件 DecisionInstall
    }

    // 2. 从 ReleaseImage bundle 填充 Current (install.components + upgrade.components)
    upgrade.FillCurrentFromBundle(vc, currentBundle)

    // 3. 清除节点级组件的 Current (保持为空, 使三层机制生效)
    //    第一层 (B): Current=="" → NeedsExecution=true → 组件执行
    //    第一层 (C): Condition 求值 (ScaleType="master"/"worker") → Master/Worker 组件选择性执行
    //    第二层: NeedExecute(StateCode) → 前置守卫, 有节点需要则继续
    //    第三层: Execute 内部 StateCode 位标记 → 精确过滤目标节点
    for _, name := range vc.TargetNames() {
        if isNodeScopedComponent(name) {
            vc.SetCurrent(name, "")
        }
    }
}

// isNodeScopedComponent 判断组件是否为节点级 (需要在每个节点上独立执行)
func isNodeScopedComponent(name string) bool {
    switch name {
    case upgrade.ComponentBKEAgent,
        upgrade.ComponentContainerd,
        upgrade.ComponentKubernetesMaster,
        upgrade.ComponentKubernetesWorker,
        upgrade.ComponentEtcd:
        return true
    default:
        return false
    }
}

// isScale 判断是否为扩容场景
func (r *BKEClusterReconciler) isScale(old, new *bkev1beta1.BKECluster) bool {
    oldNodeCount := len(old.Spec.Nodes)
    newNodeCount := len(new.Spec.Nodes)
    return newNodeCount > oldNodeCount
}
```

**EnsureMasterInit 幂等改造**：

```go
// pkg/phaseframe/phases/ensure_master_init.go — 幂等改造

func (e *EnsureMasterInit) Execute() (ctrl.Result, error) {
    bkeCluster := e.Ctx.BKECluster
    bkeNodes, _ := fetchBKENodesIfCPInitialized(e.Ctx, bkeCluster)

    // ★ 判断是 init 还是 join
    existingMasters := getExistingMasterNodes(bkeNodes)
    isNewMaster := len(existingMasters) == 0

    if isNewMaster {
        return e.executeMasterInit()
    }
    return e.executeMasterJoin()
}

// getExistingMasterNodes 从 BKENodes 中筛选已就绪的 Master 节点
func getExistingMasterNodes(bkeNodes bkev1beta1.BKENodes) []confv1beta1.BKENode {
    var masters []confv1beta1.BKENode
    for _, n := range bkeNodes {
        if !slices.Contains(n.Spec.Role, node.MasterNodeRole) &&
           !slices.Contains(n.Spec.Role, node.MasterWorkerNodeRole) {
            continue
        }
        if n.Status.State == confv1beta1.NodeProvisioned ||
           n.Status.State == confv1beta1.NodeReady {
            masters = append(masters, n)
        }
    }
    return masters
}

// executeMasterJoin 后续 Master 加入逻辑 (从 EnsureMasterJoin 迁移)
// 版本信息从 ReleaseImage bundle 获取, 不再依赖 BKECluster.Spec
func (e *EnsureMasterInit) executeMasterJoin() (ctrl.Result, error) {
    ctx, c, bkeCluster, _, log := e.Ctx.Untie()

    // 1. 从 ReleaseImage bundle 获取版本信息
    bundle := e.Ctx.ReleaseBundle
    k8sVersion := releaseVersionFromBundle(bundle, upgrade.ComponentKubernetesMaster)
    etcdVersion := releaseVersionFromBundle(bundle, upgrade.ComponentEtcd)

    // 2. 获取需要 join 的 Master 节点 (StateCode 过滤: !NodeBootFlag && !MasterInitFlag)
    bkeNodes, ok := fetchBKENodesIfCPInitialized(e.Ctx, bkeCluster)
    if !ok {
        return ctrl.Result{Requeue: true}, nil
    }
    needJoinNodes := phaseutil.GetNeedJoinMasterNodesWithBKENodes(bkeCluster, bkeNodes)
    if len(needJoinNodes) == 0 {
        return ctrl.Result{}, nil
    }

    // 3. 调整 KubeadmControlPlane 副本数 (触发 CAPI 创建新 Machine)
    scope, err := phaseutil.GetClusterAPIAssociateObjs(ctx, c, e.Ctx.Cluster)
    if err != nil || scope.KubeadmControlPlane == nil {
        return ctrl.Result{}, fmt.Errorf("get cluster-api associate objs failed: %w", err)
    }
    currentReplicas := *scope.KubeadmControlPlane.Spec.Replicas
    exceptReplicas := currentReplicas + int32(len(needJoinNodes))
    masterNodes := bkeNodes.ToNodes().Master()
    if exceptReplicas > int32(masterNodes.Length()) {
        exceptReplicas = int32(masterNodes.Length())
    }

    if err := phaseutil.UpdateKubeadmControlPlaneReplicas(ctx, c, scope.KubeadmControlPlane, exceptReplicas); err != nil {
        return ctrl.Result{}, fmt.Errorf("scale up KCP failed: %w", err)
    }
    if err := phaseutil.ResumeClusterAPIObj(ctx, c, scope.KubeadmControlPlane); err != nil {
        return ctrl.Result{}, fmt.Errorf("resume KCP failed: %w", err)
    }

    // 4. 等待节点加入 (轮询 Machine.Status.NodeRef)
    if err := e.waitMasterJoin(len(needJoinNodes), needJoinNodes); err != nil {
        _ = phaseutil.UpdateKubeadmControlPlaneReplicas(ctx, c, scope.KubeadmControlPlane, currentReplicas)
        return ctrl.Result{}, fmt.Errorf("wait master join failed: %w", err)
    }

    // 5. 同步节点状态
    if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
        return ctrl.Result{}, fmt.Errorf("sync cluster status failed: %w", err)
    }
    return ctrl.Result{}, nil
}

// waitMasterJoin 轮询等待所有新 Master 节点加入完成
func (e *EnsureMasterInit) waitMasterJoin(nodesCount int, nodesToJoin bkenode.Nodes) error {
    ctx, c, bkeCluster, _, log := e.Ctx.Untie()
    timeOut, err := phaseutil.GetBootTimeOut(bkeCluster)
    if err != nil { log.Warn("get boot timeout failed: %v", err) }
    waitTime := time.Duration(nodesCount) * timeOut
    ctxTimeout, cancel := context.WithTimeout(ctx, waitTime)
    defer cancel()

    successJoinNode := make(map[int]confv1beta1.Node)
    err = waitForNodesJoin(WaitForNodesJoinParams{
        Ctx: ctx, Client: c, BKECluster: bkeCluster,
        NodesToJoin: nodesToJoin, Log: log,
        Timeout: ctxTimeout, SuccessJoinNode: successJoinNode,
    })
    if errors.Is(err, wait.ErrWaitTimeout) {
        return errors.Errorf("wait master join timeout")
    }
    return err
}
```

#### 扩容触发流程示例

以 3 节点集群（master-1, worker-1, worker-2）扩容新增 master-2 为例：

**Legacy PhaseFlow 路径（当前代码）**:

```
前置状态:
  master-1: StateCode = NodeAgentPushedFlag|NodeAgentReadyFlag|NodeEnvFlag|NodeBootFlag|MasterInitFlag
  worker-1: StateCode = NodeAgentPushedFlag|NodeAgentReadyFlag|NodeEnvFlag|NodeBootFlag
  worker-2: StateCode = NodeAgentPushedFlag|NodeAgentReadyFlag|NodeEnvFlag|NodeBootFlag
  master-2: StateCode = 0 (新增节点, 无任何位标记)

PhaseFlow.CalculatePhase() 遍历 DeployPhases (11 个 Phase):
  → EnsureBKEAgent.NeedExecute()
    → HasNodesNeedingPhase(NodeAgentPushedFlag)
    → master-2: NodeAgentPushedFlag 未设置 → true ★ 加入执行列表
  → EnsureNodesEnv.NeedExecute()
    → HasNodesNeedingPhase(NodeEnvFlag)
    → master-2: NodeEnvFlag 未设置 → true ★ 加入执行列表
  → EnsureCerts.NeedExecute()
    → 证书已存在 → false → 跳过
  → EnsureClusterAPIObj.NeedExecute()
    → CAPI 对象已存在 → false → 跳过
  → EnsureLoadBalance.NeedExecute()
    → LB 已配置 → false → 跳过
  → EnsureMasterInit.NeedExecute()
    → 有新 Master → true ★ 加入执行列表
  → EnsureMasterJoin.NeedExecute()
    → GetNeedJoinMasterNodesWithBKENodes() → master-2 未加入 → true ★ 加入执行列表
  → EnsureWorkerJoin.NeedExecute()
    → 无新 Worker → false → 跳过
  → EnsureAddonDeploy.NeedExecute()
    → Addon 已部署 → false → 跳过
  → EnsureNodesPostProcess.NeedExecute()
    → HasNodesNeedingPhase(NodePostProcessFlag) → master-2 未处理 → true ★ 加入执行列表
  → EnsureAgentSwitch.NeedExecute()
    → 已切换 → false → 跳过

PhaseFlow.Execute() 按顺序执行:
  1. EnsureBKEAgent → GetNeedPushAgentNodesWithBKENodes() (filterNodes 模式) → 只操作 master-2
      → 推送 bkeagent → 设置 master-2 的 NodeAgentPushedFlag|NodeAgentReadyFlag
  2. EnsureNodesEnv → getNodesToInitEnv() (内联循环模式) → 只操作 master-2 (NodeEnvFlag 未设置)
      → 安装 containerd/系统配置 → 设置 master-2 的 NodeEnvFlag
  3. EnsureMasterInit → getExistingMasterNodes() → master-1 已就绪 → 执行 join
      → GetNeedJoinMasterNodesWithBKENodes() (filterNodes 模式) → master-2 未加入 → 执行 kubeadm join
      → 设置 master-2 的 NodeBootFlag
  4. EnsureNodesPostProcess → 只操作 master-2
      → 执行后置脚本 → 设置 master-2 的 NodePostProcessFlag
```

**DAG 化路径（目标设计）**:

```
前置状态: (同上)

executeScaleDAG:
  1. fillCurrentFromExistingNodes 填充 VersionContext:
     集群级组件 (certs, coredns, ...): Current==Target → Skip
     节点级组件 (bkeagent, containerd, ...): Current=="" → Install
  1a. 设置 Operation="scale", ScaleType="master" (Master 扩容) 到 TemplateContext

  2. Scheduler.ExecuteDAG:

  第一层: Scheduler 组件级 Decision (VersionContext + Condition)
    (A) shouldSkipComponent → 集群级 IsCompleted → false (扩容首次)
    (B) componentNeedsUpgrade (VersionContext):
        → 集群级 Current==Target → Skip (certs, coredns, kube-proxy)
        → 节点级 Current=="" → NeedsExecution=true (bkeagent, containerd, kubernetes-master)
    (C) shouldExecuteByCondition (KEP-18):
        → bkeagent: 无 Condition → 跳过此层, 执行
        → containerd: 无 Condition → 跳过此层, 执行
        → kubernetes-master: Condition '{{ eq .ScaleType "master" }}'
            ScaleType="master" → 求值 "true" → 执行 ★
        → kubernetes-worker: Condition '{{ eq .ScaleType "worker" }}'
            ScaleType="master" → 求值 "false" → Skip ★ (Worker 扩容时才执行)
    (D) executeComponent → 分发到 Executor

  第二层: PhaseRunner.NeedExecute 前置守卫 (StateCode) — runner.go:40
    → EnsureBKEAgent.NeedExecute()
      → HasNodesNeedingPhase(NodeAgentPushedFlag)
      → master-2: 未设置 → true → 继续 Execute
    → EnsureNodesEnv.NeedExecute()
      → HasNodesNeedingPhase(NodeEnvFlag)
      → master-2: 未设置 → true → 继续 Execute
    → EnsureCerts.NeedExecute()
      → 证书已存在 → false → return nil (跳过 Execute)
    → EnsureMasterInit.NeedExecute()
      → 有新 Master → true → 继续 Execute

  第三层: inline handler Execute 内部精确过滤 (StateCode)
    EnsureBKEAgent.Execute()
      → GetNeedPushAgentNodesWithBKENodes() (filterNodes 模式)
        predicate = !NodeAgentPushedFlag
        master-1: 已设置 → 跳过
        worker-1/2: 已设置 → 跳过
        master-2: 未设置 → 执行推送 ★
      → 推送完成后设置 master-2 的 NodeAgentPushedFlag|NodeAgentReadyFlag

    EnsureNodesEnv.Execute()
      → getNodesToInitEnv() (内联循环模式)
        检查: !NodeEnvFlag && NodeAgentReadyFlag
        master-1: NodeEnvFlag 已设置 → 跳过
        worker-1/2: NodeEnvFlag 已设置 → 跳过
        master-2: NodeEnvFlag 未设置, NodeAgentReadyFlag 已设置 → 执行初始化 ★
      → 初始化完成后设置 master-2 的 NodeEnvFlag

    EnsureMasterInit.Execute()
      → getExistingMasterNodes(): master-1 已就绪 → 执行 join (非 init)
      → GetNeedJoinMasterNodesWithBKENodes() (filterNodes 模式)
        predicate = !NodeBootFlag && !MasterInitFlag
        master-1: NodeBootFlag 已设置 → 跳过
        master-2: NodeBootFlag 未设置 → 执行 join ★
      → 调整 KCP 副本数 → 等待 master-2 加入
      → join 完成后设置 master-2 的 NodeBootFlag
```

### 10.4 集群删除/重置 DAG 化

#### 设计思路

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
│    1. 无逆序卸载: 不区分组件类型 (inline/yaml/helm/binary), 不按依赖逆序卸载    │
│    2. 无组件级清理: CAPI 级联删除 Machine, 但节点上的组件 (containerd/etcd)    │
│       不被显式卸载 — 靠 Machine 销毁后 OS 上的组件残留                           │
│    3. 无 helm 卸载: helm 组件 (如 coredns) 通过 CAPI Machine 删除间接清理,       │
│       不执行 helm uninstall                                                      │
│    4. 无 binary 卸载: binary 组件 (如 containerd/bkeagent) 不执行 UninstallScript │
│       仅通过 ShutDownAgent 关闭 bkeagent 进程, containerd/etcd 等不清理          │
│                                                                                 │
│  DAG 化方案:                                                                     │
│    构建卸载 DAG (安装 DAG 的逆序)，复用同一组件声明:                             │
│    安装: bkeagent → nodes-env → certs → ... → agent-switch (正序)               │
│    卸载: agent-switch → ... → certs → nodes-env → bkeagent (逆序)              │
│                                                                                 │
│  各组件类型的卸载方式:                                                           │
│    inline: 调用 handler 的 Uninstall/Reset 逻辑 (如 kubeadm reset)             │
│    yaml: YamlInstaller.DeleteComponent (kubectl delete 清单中所有资源)         │
│    helm: HelmInstaller.Uninstall (helm uninstall 释放 Helm Release)            │
│    binary: BinaryInstaller.Uninstall (SSH 执行 UninstallScript, 停止服务)      │
│                                                                                 │
│  核心设计:                                                                       │
│  1. 逆序 DAG — 安装 DAG 反转依赖边，先卸载依赖组件，再卸载被依赖组件            │
│  2. UninstallExecutor — 每种类型的执行器新增 Uninstall 方法                      │
│  3. 非阻塞 — 卸载不阻塞 (某组件卸载失败不阻止后续组件卸载)                      │
│  4. Legacy 共存 — DeletePhases 在 Phase 4 前仍可用, DAG 路径通过 Feature Gate 切换 │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### Legacy DeletePhases 详细说明（当前代码）

**DeletePhases 定义**（`list.go:81-84`）：

```go
DeletePhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsurePaused,           // 1. 集群管理暂停
    NewEnsureDeleteOrReset,    // 2. 集群删除/重置
}
```

**EnsureDeleteOrReset.Execute() 内部逻辑**（`ensure_delete_or_reset.go:73`）：

```go
func (e *EnsureDeleteOrReset) Execute() (ctrl.Result, error) {
    // 1. 如果集群暂停, 先恢复并清除所有 Command
    if e.Ctx.BKECluster.Spec.Pause { ... }

    // 2. 轮询执行 reconcileDelete (超时 60 分钟)
    err := wait.PollImmediateUntil(..., func() (bool, error) {
        return e.reconcileDelete(ctx) == nil, nil
    }, ctx.Done())
}

func (e *EnsureDeleteOrReset) reconcileDelete(ctx context.Context) error {
    // 2a. 设置 ClusterStatus = ClusterDeleting
    e.ensureClusterStatusDeleting(...)

    // 2b. 删除 CAPI Cluster 对象 (级联删除所有 Machine)
    //     → Machine 删除触发节点上的 kubeadm reset (CAPI Machine controller)
    e.handleClusterDeletion(...)

    // 2c. 等待所有 BKEMachine 删除完成
    //     手动删除未 bootstrap 的 Machine, 移除 finalizer
    e.handleBKEMachineDeletion(...)

    // 2d. 删除关联资源: Secret, Command
    e.deleteRelatedResources(...)

    // 2e. SSH 关闭所有节点上的 bkeagent
    //     发送 Shutdown 命令到 bkeagent (仅关闭进程, 不卸载)
    e.ShutDownAgent(ctx)

    // 2f. 清理集群资源: BKENode, Event, finalizer
    e.cleanupClusterResources(...)

    // 2g. 删除命名空间 (可选, 根据 annotation)
    e.handleNamespaceDeletion(...)
}
```

**Legacy DeletePhases 的卸载覆盖范围**：

| 组件类型 | 卸载方式 | 覆盖情况 | 说明 |
|---------|---------|---------|------|
| **inline (kubeadm)** | CAPI Machine 删除触发 kubeadm reset | ✅ 间接 | Machine controller 在节点上执行 kubeadm reset |
| **yaml (kube-proxy, coredns)** | CAPI Machine 删除 → 节点移除 → 资源孤立 | ❌ 不显式 | 不执行 kubectl delete, 依赖 Machine 销毁 |
| **helm** | 不卸载 | ❌ 缺失 | 不执行 helm uninstall, Helm Release 残留 |
| **binary (containerd, bkeagent)** | bkeagent 关闭进程 | ❌ 不完整 | 仅 ShutdownAgent 关闭 bkeagent; containerd/etcd 不卸载 |

**Legacy 删除/重置的缺陷**：

1. **无组件级卸载**：全部清理逻辑在 `reconcileDelete` 中，不按组件依赖逆序卸载
2. **helm 组件不卸载**：coredns 等通过 helm 部署的组件不执行 `helm uninstall`
3. **binary 组件不卸载**：containerd/bkeagent 等不执行 `UninstallScript`，仅关闭 bkeagent 进程
4. **yaml 组件不显式删除**：不执行 `kubectl delete`，依赖 CAPI Machine 销毁后资源自然孤立

#### DAG 化方案设计

**1. 卸载 DAG 构建 — 安装 DAG 逆序**

```go
// pkg/upgrade/bundle.go — 卸载 DAG 构建

// BuildUninstallDAGFromBundle 构建卸载 DAG (安装 DAG 逆序)
func BuildUninstallDAGFromBundle(
    bundle *releasemanifest.Bundle,
    resolve topology.DependencyResolver,
) (*topology.UpgradeDAG, error) {
    // 1. 复用安装 DAG 构建
    dag, err := BuildInstallDAGFromBundle(bundle, resolve)
    if err != nil {
        return nil, err
    }
    // 2. 逆序 DAG (依赖关系反转: A→B 变为 B→A)
    return dag.Reverse(), nil
}
```

**2. DAG 逆序实现**

```go
// pkg/topology/component.go — DAG 逆序

// Reverse 返回逆序 DAG (依赖关系反转)
func (g *Graph) Reverse() *Graph {
    reversed := NewGraph()
    // 复制所有节点
    for node := range g.nodes {
        reversed.AddNode(node)
    }
    // 反转所有边: prerequisite → dependent 变为 dependent → prerequisite
    for prerequisite, dependents := range g.outEdges {
        for dependent := range dependents {
            reversed.AddEdge(dependent, prerequisite) // 反转方向
        }
    }
    return reversed
}

// UpgradeDAG.Reverse() 包装
func (d *UpgradeDAG) Reverse() *UpgradeDAG {
    reversed := &UpgradeDAG{
        graph: d.graph.Reverse(),
        nodes: make(map[string]*ComponentNode),
    }
    // 复制节点 (保持 FailurePolicy 等属性)
    for name, node := range d.nodes {
        reversed.nodes[name] = node
    }
    return reversed
}
```

**3. 各组件类型的 Uninstall 方法**

```go
// pkg/dagexec/executor.go — ComponentExecutor 新增 UninstallComponent

type ComponentExecutor interface {
    ExecuteComponent(ctx context.Context, node *topology.ComponentNode,
        execCtx *ExecutionContext) error
    UninstallComponent(ctx context.Context, node *topology.ComponentNode,
        execCtx *ExecutionContext) error  // ★ 新增
    GetComponentType() ComponentType
}

// InlineComponentExecutor — inline 组件卸载 (kubeadm reset 等)
func (e *InlineComponentExecutor) UninstallComponent(ctx context.Context,
    node *topology.ComponentNode, execCtx *ExecutionContext) error {
    // 调用 handler 的 Uninstall 逻辑 (如 kubeadm reset)
    return e.Runner.Uninstall(ctx, execCtx.Cluster, node.Inline.Handler, node.Inline.Version)
}

// YamlComponentExecutor — yaml 组件卸载 (kubectl delete)
func (e *YamlComponentExecutor) UninstallComponent(ctx context.Context,
    node *topology.ComponentNode, execCtx *ExecutionContext) error {
    // 加载 manifest → 删除清单中所有资源
    pkg, _ := e.store.GetComponentManifests(ctx, node.Name, node.Version, execCtx.TemplateContext)
    return e.applier.DeleteComponent(ctx, pkg)
}

// HelmComponentExecutor — helm 组件卸载 (helm uninstall) ★ 新增
func (e *HelmComponentExecutor) UninstallComponent(ctx context.Context,
    node *topology.ComponentNode, execCtx *ExecutionContext) error {
    // 加载 Helm Release 信息 → helm uninstall
    releaseName := fmt.Sprintf("%s-%s", execCtx.TemplateContext.ClusterName, node.Name)
    return e.helmClient.Uninstall(ctx, releaseName)
}

// BinaryComponentExecutor — binary 组件卸载 (SSH UninstallScript) ★ 新增
func (e *BinaryComponentExecutor) UninstallComponent(ctx context.Context,
    node *topology.ComponentNode, execCtx *ExecutionContext) error {
    // 1. 获取 ComponentVersion
    cv, err := e.cvStore.GetComponentVersion(ctx, node.Name, node.Version)

    // 2. 获取全部节点 (binary 是 per-node 的)
    allNodes, err := execCtx.NodeProvider.GetNodes(ctx, execCtx.Cluster)

    // 3. 逐节点 SSH 执行 UninstallScript (停止服务 + 清理二进制/配置)
    for _, targetNode := range allNodes {
        if e.statusUpdater != nil {
            e.statusUpdater.MarkPending(ctx, execCtx.Cluster, targetNode.IP, cv.Spec.Name)
        }
        opts := binaryinstaller.InstallOptions{
            Component:   cv,
            TemplateCtx: execCtx.TemplateContext,
            Action:      binaryinstaller.BinaryActionUninstall, // ★ 卸载动作
        }
        if err := e.installer.Uninstall(ctx, opts); err != nil {
            // 非阻塞: 记录失败, 继续卸载下一个节点
            execCtx.Log.Warn("uninstall %s on %s failed: %v", node.Name, targetNode.IP, err)
            continue
        }
        if e.statusUpdater != nil {
            e.statusUpdater.MarkRemoved(ctx, execCtx.Cluster, targetNode.IP, cv.Spec.Name)
        }
    }
    return nil
}
```

**4. 卸载 DAG 执行入口**

```go
// controllers/capbke/bkecluster_controller.go — 删除/重置场景入口

func (r *BKEClusterReconciler) executeUninstallDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 1. 解析当前版本 ReleaseImage bundle (卸载当前安装的版本)
    bundle, err := r.resolveCurrentReleaseBundle(ctx, newCluster)

    // 2. 构建卸载 DAG (安装 DAG 逆序)
    dag, err := upgrade.BuildUninstallDAGFromBundle(bundle, upgrade.BundleDependencyResolver(bundle))

    // 3. 构建 ComponentFactory (注册卸载 handler)
    factory := componentfactory.NewFactoryFromBundle(bundle)
    factory.RegisterUninstallHandlers() // ★ 注册卸载 handler

    // 4. 构建 Scheduler (非阻塞模式: 卸载失败不阻止后续)
    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:    NewInlinePhaseRunnerAdapter(phaseCtx, &PhaseRunner{Factory: factory}),
        ManifestStore:   manifest.NewBundleStore(bundle),
        CVStore:         manifest.NewBundleStore(bundle),
        MaxParallelPerBatch: 2,    // 低并行度
        DefaultFailurePolicy: "Continue", // ★ 卸载失败不阻塞 (与安装的 FailFast 不同)
    })

    // 5. 构建 ExecutionContext
    execCtx := buildExecutionContext(phaseCtx, oldCluster, newCluster, bkeLogger, nil)
    execCtx.TemplateContext.Operation = "rollback" // ★ 供 Condition 求值
    execCtx.UninstallMode = true                   // ★ 执行器检查此标记, 调用 Uninstall 而非 Execute

    // 6. 执行卸载 DAG (逆序)
    //    Batch 1: agent-switch → 停止 Agent 监听
    //    Batch 2: kube-proxy/coredns → kubectl delete / helm uninstall
    //    Batch 3: kubernetes-worker → kubeadm reset (worker)
    //    Batch 4: kubernetes-master → kubeadm reset (master)
    //    Batch 5: load-balance → 删除 HA
    //    Batch 6: certs → 删除证书
    //    Batch 7: containerd → SSH UninstallScript (停止服务 + 清理二进制)
    //    Batch 8: bkeagent → SSH Shutdown (关闭进程)
    if err := sched.ExecuteDAG(ctx, execCtx, dag); err != nil {
        // 非阻塞: 记录错误但不返回 (继续清理管理集群资源)
        bkeLogger.Warn("uninstall DAG completed with errors: %v", err)
    }

    // 7. 清理管理集群资源 (复用 Legacy DeletePhases 的管理集群清理逻辑)
    r.cleanupManagementClusterResources(ctx, newCluster, bkeLogger)
    // → 删除 BKENode / Secret / Command / Event / finalizer / namespace
    //   (这些是管理集群上的 CR, 不在卸载 DAG 范围内)

    return nil
}
```

**5. 卸载 DAG 各组件类型的卸载覆盖范围**

| 组件类型 | DAG 化卸载方式 | Legacy 覆盖 | 改进 |
|---------|---------------|------------|------|
| **inline** | handler Uninstall (kubeadm reset) | ✅ CAPI 间接 | ★ 显式 kubeadm reset, 不依赖 CAPI |
| **yaml** | kubectl delete 清单资源 | ❌ 缺失 | ★ 新增: 显式删除 YAML 资源 |
| **helm** | helm uninstall 释放 Release | ❌ 缺失 | ★ 新增: 显式卸载 Helm Release |
| **binary** | SSH UninstallScript | ❌ 不完整 | ★ 新增: 停止服务 + 清理二进制/配置 |

**6. 卸载 DAG 与 Legacy DeletePhases 的共存设计**

| 维度 | Legacy DeletePhases | DAG 化卸载 |
|------|--------------------|-----------| 
| Feature Gate | OFF (默认) | ON |
| 执行入口 | `determinePhasesFuncs()` → `DeletePhases` | `executeUninstallDAG()` |
| 组件级卸载 | ❌ 单体 `reconcileDelete` | ✅ 逆序 DAG 逐组件卸载 |
| helm 卸载 | ❌ | ✅ `helm uninstall` |
| binary 卸载 | ❌ 仅 ShutdownAgent | ✅ SSH `UninstallScript` |
| yaml 删除 | ❌ 依赖 CAPI 级联 | ✅ `kubectl delete` |
| 管理集群清理 | ✅ 在 `reconcileDelete` 中 | ✅ 在 `cleanupManagementClusterResources` 中 (复用) |
| 失败策略 | 阻塞 (轮询重试) | 非阻塞 (`Continue`) |

> **设计原则**：卸载 DAG 处理**目标集群上的组件卸载**（inline/yaml/helm/binary），管理集群资源的清理（BKENode/Secret/Command/Event/finalizer/namespace）复用 Legacy `reconcileDelete` 中的逻辑，通过 `cleanupManagementClusterResources` 单独调用。两者分离，职责清晰。

#### EnsureDeleteOrReset 注册为 inline 组件

Legacy `EnsureDeleteOrReset` 的 `reconcileDelete` 逻辑中，除了目标集群组件卸载外，还包含**管理集群资源清理**（删除 CAPI Cluster、BKEMachine、Secret、Command、BKENode、Event、finalizer、namespace）。将这部分管理集群清理逻辑封装为 inline 组件 `delete-cluster-resources`，作为卸载 DAG 的**最后一个组件**执行。

**设计思路**：

```txt
卸载 DAG 拓扑 (逆序 + 管理集群清理):

  Batch 1: [agent-switch]           → 停止 Agent 监听
  Batch 2: [kube-proxy, coredns]    → helm uninstall / kubectl delete
  Batch 3: [kubernetes-worker]      → kubeadm reset (worker)
  Batch 4: [kubernetes-master]      → kubeadm reset (master)
  Batch 5: [load-balance]           → 删除 HA
  Batch 6: [certs]                 → 删除证书
  Batch 7: [containerd]            → SSH UninstallScript
  Batch 8: [bkeagent]              → SSH Shutdown
  Batch 9: [delete-cluster-resources] ★ 新增 inline 组件
           → 管理集群资源清理 (复用 Legacy reconcileDelete 逻辑)
           → 删除 CAPI Cluster / BKEMachine / Secret / Command
           → 删除 BKENode / Event / finalizer / namespace
```

`delete-cluster-resources` 作为卸载 DAG 的依赖终点——所有目标集群组件卸载完成后，最后执行管理集群资源清理。

**ReleaseImage 声明**：

```yaml
# ReleaseImage install.components 新增 delete-cluster-resources 组件
install:
  components:
    - name: delete-cluster-resources
      version: v1.0.0
      inline:
        handler: EnsureDeleteOrReset
        version: v1.0.0
```

**ComponentVersion 定义**：

```yaml
apiVersion: config.openfuyao.cn/v1alpha1
kind: ComponentVersion
metadata:
  name: delete-cluster-resources-v1.0.0
spec:
  name: delete-cluster-resources
  type: inline
  version: "v1.0.0"
  inline:
    handler: EnsureDeleteOrReset
    version: "v1.0.0"
  # ★ 仅删除/重置场景执行 (KEP-18 Condition 过滤)
  condition: '{{ eq .Operation "rollback" }}'
  # 依赖所有其他组件 (作为 DAG 的依赖终点)
  dependencies:
    - name: bkeagent
    - name: containerd
    - name: kubernetes-master
    - name: kubernetes-worker
    - name: certs
    - name: agent-switch
```

**DeclarativeInstallCatalog 注册**：

```go
// DeclarativeInstallCatalog 新增
{ Name: "delete-cluster-resources", Mode: ExecutionInline,
  InlineHandler: "EnsureDeleteOrReset", LegacyPhase: "EnsureDeleteOrReset" },
```

**handler 注册**：

```go
// pkg/componentfactory/registry.go — registerInlineHandler 新增
case "EnsureDeleteOrReset":
    f.Register(handler, version, phases.NewEnsureDeleteOrReset)
```

**EnsureDeleteOrReset 改造为 inline handler**：

```go
// pkg/phaseframe/phases/ensure_delete_or_reset.go — 改造为 inline handler

type EnsureDeleteOrReset struct {
    phaseframe.BasePhase
}

func (e *EnsureDeleteOrReset) Execute() (ctrl.Result, error) {
    baseCtx := context.Background()
    _, c, bkeCluster, _, log := e.Ctx.Untie()

    // 复用现有 reconcileDelete 中的管理集群清理逻辑
    // ★ 删除目标集群组件的逻辑已由卸载 DAG 的前序组件完成 (yaml/helm/binary/inline uninstall)
    // ★ 此 handler 仅处理管理集群资源清理

    // 1. 删除 CAPI Cluster (级联删除 Machine)
    if err := e.handleClusterDeletion(baseCtx, c, log); err != nil {
        return ctrl.Result{}, err
    }

    // 2. 等待 BKEMachine 删除完成
    if err := e.handleBKEMachineDeletion(baseCtx, c, bkeCluster, log); err != nil {
        return ctrl.Result{}, err
    }

    // 3. 删除关联资源: Secret, Command
    e.deleteRelatedResources(baseCtx, c, bkeCluster, log)

    // 4. 删除 BKENode / Event / finalizer
    if err := e.cleanupClusterResources(baseCtx, c, bkeCluster, log); err != nil {
        return ctrl.Result{}, err
    }

    // 5. 删除命名空间 (可选)
    if err := e.handleNamespaceDeletion(baseCtx, c, bkeCluster, log); err != nil {
        return ctrl.Result{}, err
    }

    log.Info("cluster resources cleanup completed")
    return ctrl.Result{}, nil
}

// NeedExecute — 仅删除/重置场景执行
// (Condition '{{ eq .Operation "rollback" }}' 已在 Scheduler 层过滤,
//  此处 NeedExecute 作为第二层守卫, 检查 DeletionTimestamp / Spec.Reset)
func (e *EnsureDeleteOrReset) NeedExecute(_ *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
    if !new.DeletionTimestamp.IsZero() || new.Spec.Reset {
        e.SetStatus(bkev1beta1.PhaseWaiting)
        return true
    }
    return false
}
```

**改造前后对比**：

| 维度 | Legacy EnsureDeleteOrReset | inline 组件 delete-cluster-resources |
|------|---------------------------|--------------------------------------|
| 执行位置 | DeletePhases (PhaseFlow) | 卸载 DAG 最后一个 Batch |
| 目标集群组件卸载 | ❌ 不卸载 (依赖 CAPI 级联) | ✅ 由前序 DAG 组件完成 (yaml/helm/binary/inline) |
| 管理集群资源清理 | ✅ 在 `reconcileDelete` 中 | ✅ 复用相同逻辑 (`handleClusterDeletion` / `handleBKEMachineDeletion` / ...) |
| 触发条件 | `DeletionTimestamp` / `Spec.Reset` | `Condition: '{{ eq .Operation "rollback" }}'` + `NeedExecute(DeletionTimestamp)` |
| 执行顺序 | 独立 PhaseFlow (无依赖) | DAG 依赖终点 (所有组件卸载后执行) |
| 失败策略 | 阻塞 (轮询重试 60 分钟) | 非阻塞 (`Continue`) + PhaseRunner NeedExecute 重入 |

> **设计优势**：将 Legacy `EnsureDeleteOrReset` 从单体 Phase 拆解为卸载 DAG 的 inline 组件，管理集群清理逻辑作为 DAG 的依赖终点执行，确保目标集群组件先卸载、管理集群资源后清理，执行顺序由 DAG 拓扑保证而非硬编码。

#### EnsureDeleteOrReset 原样注册为 inline 组件（最小迁移方案）

上述"完整逆序 DAG"方案和"EnsureDeleteOrReset 注册为 inline 组件"方案都对 `EnsureDeleteOrReset` 进行了拆解改造。本方案是**最小改动迁移方案**——`EnsureDeleteOrReset` 的 `reconcileDelete` 全部逻辑保持不变，直接注册为 inline 组件，DAG 仅含一个节点。

**设计思路**：

```txt
最小迁移方案 DAG 拓扑 (单节点):

  Batch 1: [delete-cluster-resources]  ← 唯一节点, 复用 EnsureDeleteOrReset 全部逻辑
             → handleClusterDeletion (删除 CAPI Cluster, 级联删除 Machine)
             → handleBKEMachineDeletion (等待 BKEMachine 删除)
             → deleteRelatedResources (删除 Secret/Command)
             → ShutDownAgent (SSH 关闭 bkeagent)
             → cleanupClusterResources (删除 BKENode/Event/finalizer)
             → handleNamespaceDeletion (删除命名空间)

  ★ 与 Legacy DeletePhases 行为完全一致
  ★ 仅执行入口从 PhaseFlow 切换到 DAG (executeUninstallDAG)
  ★ EnsureDeleteOrReset.Execute() / reconcileDelete() 代码零改动
```

**与现有两个方案的区别**：

| 维度 | 方案 A: 完整逆序 DAG | 方案 B: 拆解为 inline 组件 | 方案 C: 原样注册 ★ |
|------|---------------------|--------------------------|-------------------|
| DAG 节点数 | 全部组件 (8+ 节点) | 全部组件 + delete-cluster-resources (9 节点) | 仅 1 个节点 (代码硬编码) |
| EnsureDeleteOrReset 改造 | 裁剪为仅管理集群清理 | 裁剪为仅管理集群清理 | **零改动** |
| 依赖 ReleaseImage | ✅ 需要 (构建逆序 DAG) | ✅ 需要 (构建逆序 DAG) | **❌ 不依赖** (代码硬编码 DAG) |
| 目标集群组件卸载 | 逐组件 Uninstall (yaml/helm/binary/inline) | 逐组件 Uninstall + 管理集群清理 | 不卸载 (依赖 CAPI 级联, 同 Legacy) |
| helm 卸载 | ✅ helm uninstall | ✅ helm uninstall | ❌ (同 Legacy, 不卸载) |
| binary 卸载 | ✅ SSH UninstallScript | ✅ SSH UninstallScript | ❌ (同 Legacy, 仅 ShutdownAgent) |
| 与 Legacy 行为一致性 | ❌ 增强 (新增卸载) | ❌ 增强 (新增卸载) | ✅ **完全一致** |
| 迁移风险 | 高 (新增 Uninstall 逻辑) | 高 (拆解 + 新增 Uninstall) | **低** (零代码改动) |
| 适用阶段 | Phase 4 (全量 DAG) | Phase 4 (全量 DAG) | **Phase 2-3 (灰度迁移)** |

**适用场景**：Phase 2-3 灰度阶段，删除场景最先迁移到 DAG 路径（风险最低），通过 Feature Gate 切换执行入口。Phase 4 再升级为完整逆序 DAG（方案 A/B），实现组件级卸载。

> **设计原则**：`delete-cluster-resources` 不注册到 ReleaseImage `install.components` 中，直接在代码中硬编码构建单节点 DAG。原因：删除/重置是基础设施操作，不属于 ReleaseImage 声明的安装组件；`EnsureDeleteOrReset` 的 `reconcileDelete` 逻辑与集群版本无关，不需要随 ReleaseImage 版本变化。

**handler 注册**：

```go
// pkg/componentfactory/registry.go — registerInlineHandler 新增
case "EnsureDeleteOrReset":
    f.Register(handler, version, phases.NewEnsureDeleteOrReset)
```

**EnsureDeleteOrReset 零改动**：

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
// (Condition '{{ eq .Operation "rollback" }}' 已在 Scheduler 层过滤,
//  此处 NeedExecute 作为第二层守卫, 检查 DeletionTimestamp / Spec.Reset)
func (e *EnsureDeleteOrReset) NeedExecute(_ *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
    if !new.DeletionTimestamp.IsZero() || new.Spec.Reset {
        e.SetStatus(bkev1beta1.PhaseWaiting)
        return true
    }
    return false
}
```

**executeUninstallDAG 实现（代码中硬编码单节点 DAG）**：

```go
// controllers/capbke/bkecluster_controller.go — 删除/重置场景入口 (最小迁移方案)

func (r *BKEClusterReconciler) executeUninstallDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 1. 直接在代码中构建单节点 DAG (不依赖 ReleaseImage)
    //    ★ delete-cluster-resources 不注册到 install.components
    //    ★ EnsureDeleteOrReset 的 reconcileDelete 逻辑与集群版本无关
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

    // 2. 构建 ComponentFactory (注册 EnsureDeleteOrReset handler)
    factory := componentfactory.NewComponentFactory()
    factory.Register("EnsureDeleteOrReset", "v1.0.0", phases.NewEnsureDeleteOrReset)

    // 3. 构建 Scheduler
    sched := dagexec.NewScheduler(dagexec.SchedulerConfig{
        InlineRunner:    NewInlinePhaseRunnerAdapter(phaseCtx, &PhaseRunner{Factory: factory}),
        CVStore:         factory, // handler 从 ComponentFactory 解析
        MaxParallelPerBatch: 1, // 单节点, 无需并行
    })

    // 4. 构建 ExecutionContext
    execCtx := buildExecutionContext(phaseCtx, oldCluster, newCluster, bkeLogger, nil)
    execCtx.TemplateContext.Operation = "rollback" // ★ 供 Condition 求值

    // 5. 执行 DAG (单节点)
    //    Batch 1: delete-cluster-resources
    //      → EnsureDeleteOrReset.Execute()
    //      → reconcileDelete() 全部逻辑 (与 Legacy 完全一致)
    return sched.ExecuteDAG(ctx, execCtx, dag)
}
```

> **为什么不依赖 ReleaseImage**：
> 1. **与集群版本无关**：`EnsureDeleteOrReset` 的 `reconcileDelete` 逻辑是基础设施操作（删除 CAPI 对象、清理 CR、关闭 Agent），不随集群版本变化，不需要随 ReleaseImage 版本演进
> 2. **避免 ReleaseImage 污染**：`install.components` 语义是"安装到目标集群的组件"，`delete-cluster-resources` 是管理集群清理操作，不属于安装语义
> 3. **减少外部依赖**：不依赖 ReleaseImage bundle 解析，删除场景不需要 ReleaseImage 就绪即可执行（与 Legacy DeletePhases 行为一致——DeletePhases 不消费 ReleaseImage）

**与 Legacy DeletePhases 的能力一致性验证**：

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

> **设计优势**：零代码改动，`EnsureDeleteOrReset` 的 `reconcileDelete` 全部逻辑原样复用。迁移仅涉及执行入口切换（PhaseFlow → DAG），行为与 Legacy 完全一致，风险最低。适合 Phase 2-3 灰度阶段先行迁移删除场景，Phase 4 再升级为完整逆序 DAG（方案 A/B）。

### 10.5 DryRun 模式 DAG 化

#### 设计思路

DryRun 是指仅模拟执行，不实际修改集群。DAG 化后 DryRun 照常构建和遍历 DAG，但各执行器检查 `DryRun` 标记后仅打印不执行。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              DryRun DAG 化设计思路                                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    EnsureDryRun Phase (独立 Phase)                                              │
│      → 仅打印将要执行的 Phase 列表，不执行任何操作                               │
│    问题: DryRun 逻辑独立于安装/升级，无法利用 DAG 的依赖排序展示执行顺序        │
│                                                                                 │
│  DAG 化方案:                                                                     │
│    在 ExecutionContext 中传递 DryRun 标记:                                       │
│    DAG 照常构建 + 拓扑排序 + 遍历                                               │
│    各执行器检查 DryRun 标记 → 仅打印不执行                                       │
│                                                                                 │
│  核心优势:                                                                       │
│  1. 依赖可视化 — DryRun 输出 DAG 拓扑结构和执行顺序 (用户能看到组件依赖)       │
│  2. 版本预检 — DryRun 时 VersionContext.Decide() 正常工作 (显示哪些会跳过)    │
│  3. 统一路径 — DryRun 和实际执行走同一 DAG，不额外维护 DryRun 逻辑             │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 代码实现

```go
// pkg/dagexec/execution_context.go — 新增 DryRun 字段

type ExecutionContext struct {
    // ... 现有字段 ...
    DryRun       bool   // 🆕新增: DryRun 模式标记
    UninstallMode bool  // 🆕新增: 卸载模式标记 (§10.4)
}
```

```go
// pkg/dagexec/scheduler.go — executeComponent 检查 DryRun

func (s *Scheduler) executeComponent(
    ctx context.Context,
    node *topology.ComponentNode,
    execCtx *ExecutionContext,
) error {
    // ★ DryRun 检查
    if execCtx.DryRun {
        decision := upgrade.Decide(execCtx.VersionContext, node.Name)
        log.Info("[DryRun] component execution plan",
            "component", node.Name,
            "version", node.Version,
            "type", node.Inline.Handler,
            "decision", decision, // Install / Upgrade / Skip
            "dependencies", node.Dependencies,
        )
        return nil // 不实际执行
    }

    // 实际执行 (现有逻辑)
    cv := s.CVStore.GetComponentVersion(ctx, node.Name, node.Version)
    // ...
}
```

```go
// controllers/capbke/bkecluster_controller.go — DryRun 场景入口

// executeDryRunDAG 与 executeInstallDAG 逻辑相同，仅设置 DryRun 标记
// ★ executeInstallDAG 不需要新增 DryRun 参数，DryRun 标记在 ExecutionContext 上设置
func (r *BKEClusterReconciler) executeDryRunDAG(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) error {
    // 1-6. 与 executeInstallDAG 完全相同 (解析 bundle / 构建 VC / 构建 DAG / 注册 handler / 构建 Scheduler)
    // ...

    // 7. 构建 ExecutionContext
    execCtx := buildExecutionContext(phaseCtx, oldCluster, newCluster, bkeLogger, targetClient)
    execCtx.TemplateContext.Operation = "install"

    // ★ 唯一区别: 设置 DryRun 标记
    execCtx.DryRun = true

    // 8. 执行 DAG — 各执行器检查 DryRun 标记后仅打印不执行
    return sched.ExecuteDAG(ctx, execCtx, dag)
}
```

### 10.6 集群暂停 DAG 化

#### 设计思路

暂停是指临时停止对集群的所有操作。暂停不需要 DAG 化 — 它的语义是"不执行任何操作"，DAG 和 PhaseFlow 都需要跳过。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              集群暂停设计思路                                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Legacy PhaseFlow:                                                              │
│    EnsurePaused Phase → 设置 ClusterStatus=Paused，不执行后续 Phase             │
│                                                                                 │
│  DAG 化方案:                                                                     │
│    暂停不需要 DAG 化 — 在 reconcileCluster 入口处前置检查:                      │
│    if bkeCluster.Spec.Pause → 设置 Status=Paused → return (不构建 DAG)        │
│                                                                                 │
│  原因:                                                                           │
│  暂停的语义是"不执行任何操作"，不需要组件级的依赖排序和并行执行                   │
│  前置检查即可满足需求，无需 DAG 遍历                                             │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 代码实现

```go
// controllers/capbke/bkecluster_controller.go — 暂停前置检查

func (r *BKEClusterReconciler) reconcileCluster(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    // ★ 暂停检查 (最优先，在任何 DAG 构建之前)
    if newCluster.Spec.Pause {
        newCluster.Status.ClusterStatus = bkev1beta1.ClusterPaused
        return ctrl.Result{}, nil // 不执行任何操作
    }

    // 场景判断 (无 Legacy 回退)
    switch {
    case isDeleteOrReset(newCluster):
        return r.executeUninstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    // ...
    }
}
```

### 10.7 移除后的执行入口

完全移除 Legacy PhaseFlow 后，执行入口简化为场景分发 (无 PhaseFlow 回退)：

```go
func (r *BKEClusterReconciler) reconcileCluster(
    ctx context.Context,
    phaseCtx *phaseframe.PhaseContext,
    oldCluster, newCluster *bkev1beta1.BKECluster,
) (ctrl.Result, error) {
    // ─── 前置检查 (最优先) ───
    // 暂停: 不执行任何操作
    if newCluster.Spec.Pause {
        newCluster.Status.ClusterStatus = bkev1beta1.ClusterPaused
        return ctrl.Result{}, nil
    }

    // ─── 场景判断 (无 Legacy 回退) ───
    switch {
    // 1. 删除/重置 → 卸载 DAG (逆序)
    case isDeleteOrReset(newCluster):
        return ctrl.Result{}, r.executeUninstallDAG(ctx, phaseCtx, oldCluster, newCluster)

    // 2. 纳管 → 纳管 DAG (manage 组件探测版本，后续组件 Decide 判断)
    //    ★ 纳管优先于扩容和升级，因为纳管时先探测版本再决定后续操作
    case isManage(newCluster):
        return ctrl.Result{}, r.executeManageDAG(ctx, phaseCtx, oldCluster, newCluster)

    // 3. DryRun → 安装 DAG (仅打印不执行)
    case isDryRun(newCluster):
        return ctrl.Result{}, r.executeDryRunDAG(ctx, phaseCtx, oldCluster, newCluster)

    // 4. 扩容 → 安装 DAG (VersionContext 自动过滤已有节点)
    case isScale(oldCluster, newCluster):
        return ctrl.Result{}, r.executeScaleDAG(ctx, phaseCtx, oldCluster, newCluster)

    // 5. 升级 → 升级 DAG
    case r.shouldUseDeclarativeUpgrade(newCluster):
        return ctrl.Result{}, r.executeUpgradeDAG(ctx, phaseCtx, oldCluster, newCluster)

    // 6. 默认 → 全新安装 DAG
    default:
        return ctrl.Result{}, r.executeInstallDAG(ctx, phaseCtx, oldCluster, newCluster)
    }

    // 不再有 PhaseFlow 回退
}
```

**场景判断优先级**：

| 优先级 | 场景 | 判断条件 | 执行路径 | 说明 |
|--------|------|---------|---------|------|
| 0 | 暂停 | `Spec.Pause` | 不执行 | 最优先，任何操作都不执行 |
| 1 | 删除/重置 | `Spec.Reset` 或 `DeletionTimestamp` | 卸载 DAG (逆序) | 优先于其他操作 |
| 2 | 纳管 | `Spec.Manage` | 纳管 DAG (manage 探测版本) | 优先于扩容/升级，纳管时先探测再决定 |
| 3 | DryRun | `Spec.DryRun` | 安装 DAG (DryRun) | 优先于扩容/升级 |
| 4 | 扩容 | 新增节点 | 安装 DAG (过滤已有节点) | 优先于升级 |
| 5 | 升级 | `upgrade-ready` annotation | 升级 DAG | — |
| 6 | 安装 | 默认 | 安装 DAG | 兜底 |




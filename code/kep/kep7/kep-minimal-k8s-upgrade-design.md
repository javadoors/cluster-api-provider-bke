# KEP-7: K8s 核心组件最小化升级方案 — 保留 inline kubernetes-master 的轻量级实现

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-7 |
| **标题** | K8s 核心组件最小化升级方案 — 保留 inline kubernetes-master 的轻量级偏差门控 |
| **状态** | `provisional` |
| **类型** | Feature Design |
| **作者** | openFuyao Team |
| **创建日期** | 2026-09-02 |
| **依赖** | KEP-5 声明式升级框架、KEP-14 迭代式升级方案 (参考) |
| **参考** | KEP-14 K8s 核心组件迭代式升级方案设计 |

## 1. 摘要

本提案基于 KEP-14 (K8s 核心组件迭代式升级方案) 的偏差门控与 kubelet 延迟升级思路，提出一种**不重构现有 `kubernetes-master` inline 组件**的最小化实现方案。KEP-14 要求将 `kubernetes-master` 黑盒拆分为 7 个独立组件 (etcd/apiserver/cm/scheduler/kubelet/kubectl/kube-proxy)，涉及大量 ReleaseImage 重构、ComponentVersion 声明变更、DAG 依赖重建、执行器开发等工作 (预估 35 人日)。本方案的核心思路是：**保留 `kubernetes-master` 作为 inline 组件不变，仅在其执行前后增加偏差门控逻辑和独立的 kubelet 补充升级环节**，以最小代码修改实现 KEP-14 的核心收益 (kubelet 延迟升级 + 偏差安全 + 多 hop 编排)。具体做法是：(1) 在 ClusterVersionReconciler 中新增多 hop 编排逻辑，每个 hop 仍通过 `upgrade-ready` 注解驱动 BKEClusterReconciler 执行现有 DAG (含 `kubernetes-master` inline handler)；(2) 在 `EnsureMasterUpgrade` Phase 中增加 `skipKubeletUpgrade` 参数，通过 VersionContext 将 kubelet Target 设为 Current 来跳过 kubeadm 内的 kubelet 升级步骤；(3) 在 hop 之间增加偏差门控，当 kubelet 偏差达到极限 (3) 时触发独立的 kubelet 补充升级 (通过现有 `EnsureWorkerUpgrade` Phase 或新增轻量级 kubelet 升级命令)。预估工作量 12 人日 (vs KEP-14 的 35 人日)。

---

## 2. 动机

### 2.1 KEP-14 的重构成本

KEP-14 的核心方案是拆解 `kubernetes-master` 黑盒，将 7 个 K8s 核心组件变为独立 DAG 节点。这需要：

| 重构项 | 工作量 | 说明 |
|--------|--------|------|
| ReleaseImage 重构 | 5 人日 | 拆分 kubernetes-master 为 7 个独立组件 + composite 类型 + kubernetesVersion 字段 |
| ComponentVersion 声明 | 3 人日 | 为每个组件编写 ComponentVersion YAML + versionSkew 声明 |
| StaticPodInstaller | 5 人日 | 新增 Static Pod 类型执行器 (KEP-9) |
| BinaryExecutor (kubelet) | 3 人日 | kubelet 独立 binary 执行器 (KEP-13) |
| DAG 依赖重建 | 3 人日 | 7 个组件的依赖关系声明 + 拓扑排序 |
| 偏差约束内化 | 2 人日 | versionSkew 从外部规则表迁移到 ComponentVersion.spec |
| 控制器多 hop 重构 | 4 人日 | orchestrateMultiHopUpgrade + executeControlPlaneHop 重构 |
| 集成测试 | 6 人日 | 7 个独立组件的 E2E 测试 |
| **合计** | **35 人日** | 约 1.5 人月 |

### 2.2 最小化方案的目标

| 目标 | 说明 |
|------|------|
| **保留 kubernetes-master inline** | 不拆分黑盒，不新增 StaticPodInstaller/BinaryExecutor，不重构 ReleaseImage |
| **实现 kubelet 延迟升级** | 通过 VersionContext 跳过 kubeadm 内的 kubelet 升级步骤 |
| **实现偏差门控** | 在 hop 之间检查 kubelet vs apiserver 偏差，达到极限时触发补充升级 |
| **实现多 hop 编排** | 在 ClusterVersionReconciler 中新增多 hop 编排逻辑 |
| **最小代码修改** | 仅修改 3 个文件 + 新增 1 个文件，预估 12 人日 |

### 2.3 非目标

1. 不拆分 `kubernetes-master` 为独立组件 (KEP-14 的完整方案)
2. 不新增 StaticPodInstaller / BinaryExecutor (KEP-9 / KEP-13 范围)
3. 不重构 ReleaseImage 中 K8s 组件声明结构
4. 不内化 versionSkew 到 ComponentVersion.spec (保留外部 K8sSkewConstraints)
5. 不实现 composite 类型组件

---

## 3. 与 KEP-14 的对比

### 3.1 方案对比

| 维度 | KEP-14 (完整重构) | 本方案 (最小化) |
|------|-------------------|----------------|
| **kubernetes-master** | 拆分为 7 个独立组件 | **保留 inline 不变** |
| **ReleaseImage** | 重构为 composite + 7 个子组件 | **不变** (kubernetes-master 仍是 inline) |
| **DAG 结构** | 7 个独立 DAG 节点 + 依赖排序 | **不变** (kubernetes-master 仍是单个 DAG 节点) |
| **kubelet 延迟** | kubelet 独立节点，VersionContext 跳过 | **kubeadm 内跳过** (skipKubeletUpgrade 参数) |
| **偏差门控** | VersionSkewChecker 检查 7 个组件 | **仅检查 kubelet vs apiserver** (2 个组件) |
| **kubelet 补充升级** | BinaryExecutor 独立执行 | **复用 EnsureWorkerUpgrade** 或轻量级命令 |
| **apiserver/cm/scheduler 可见性** | 独立版本追踪 | **仍归为 KubernetesVersion** |
| **工作量** | 35 人日 | **12 人日** |
| **修改文件数** | ~15 个 | **4 个** |

### 3.2 收益对比

| 收益 | KEP-14 | 本方案 | 说明 |
|------|--------|--------|------|
| **kubelet 延迟升级** | ✅ 完全实现 | ✅ 实现 | 两者核心收益一致 |
| **偏差门控** | ✅ 7 组件全检查 | ✅ 仅 kubelet vs apiserver | 本方案覆盖最关键的偏差 |
| **多 hop 编排** | ✅ 完全实现 | ✅ 实现 | 两者编排逻辑一致 |
| **控制面快速推进** | ✅ apiserver 不等 kubelet | ✅ 同左 | 核心收益一致 |
| **组件级版本追踪** | ✅ 7 个独立版本 | ❌ 仍为 KubernetesVersion | 本方案不追踪子组件版本 |
| **偏差约束内化** | ✅ versionSkew 在 ComponentVersion | ❌ 保留外部 K8sSkewConstraints | 本方案不内化 |
| **composite 类型** | ✅ kubernetes-core 组合 | ❌ 无 | 本方案不需要 |
| **StaticPodInstaller** | ✅ 新增 | ❌ 无 | 本方案不新增 |

### 3.3 取舍分析

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              KEP-14 vs 本方案 取舍分析                                           │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  KEP-14 (完整重构):                                                             │
│  优势:                                                                          │
│  • 组件级版本追踪 (7 个独立版本)                                                │
│  • 偏差约束内化到 ComponentVersion                                              │
│  • composite 类型简化 ReleaseImage                                              │
│  • StaticPodInstaller 支持独立 manifest 替换                                    │
│                                                                                 │
│  代价:                                                                          │
│  • 35 人日工作量 (~1.5 人月)                                                    │
│  • 修改 ~15 个文件                                                              │
│  • ReleaseImage 格式变更 (向后兼容风险)                                         │
│  • 需要开发 StaticPodInstaller (KEP-9) + BinaryExecutor (KEP-13)              │
│  • 5 阶段迁移 (v2.7.0 → v3.1.0)                                                │
│                                                                                 │
│  本方案 (最小化):                                                               │
│  优势:                                                                          │
│  • 12 人日工作量 (KEP-14 的 1/3)                                                │
│  • 修改 4 个文件                                                                │
│  • ReleaseImage 格式不变 (零兼容风险)                                           │
│  • 不依赖 KEP-9/KEP-13 (可独立交付)                                             │
│  • 1 阶段迁移 (v2.7.0 即可启用)                                                 │
│                                                                                 │
│  代价:                                                                          │
│  • apiserver/cm/scheduler 仍无独立版本追踪                                      │
│  • 偏差约束仍为外部规则表 (未内化)                                              │
│  • kubernetes-master 仍是黑盒 (内部升级顺序由 kubeadm 控制)                     │
│  • 后续如需组件级追踪仍需 KEP-14 完整重构                                       │
│                                                                                 │
│  结论:                                                                          │
│  本方案以 1/3 的成本实现 KEP-14 的核心收益 (kubelet 延迟 + 偏差门控 + 多 hop)  │
│  适合作为 v2.7.0 的快速交付方案，后续可在 v3.0.0 规划 KEP-14 完整重构           │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## 4. 设计方案

### 4.1 核心思路

**保留 `kubernetes-master` 作为 inline 组件不变，通过三层增强实现 kubelet 延迟升级：**

1. **编排层** (ClusterVersionReconciler)：新增多 hop 编排逻辑，每个 hop 仍通过 `upgrade-ready` 注解驱动 BKEClusterReconciler 执行 DAG
2. **执行层** (EnsureMasterUpgrade)：新增 `skipKubeletUpgrade` 参数，通过 kubeadm 命令参数跳过 kubelet 二进制升级步骤
3. **补充层** (新增 kubeletCatchup)：偏差达到极限时，触发独立的 kubelet 补充升级 (复用现有 worker 升级机制)

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              最小化方案架构                                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  用户: kubectl patch clusterversion --desired-version v2.7.0                    │
│        (openFuyao v2.6.0, K8s v1.34 → v2.7.0, K8s v1.36, 跨 2 个 K8s 小版本)  │
│                                                                                 │
│  ┌──────────────────────────────────────────────────────────────────────────┐   │
│  │  ClusterVersionReconciler (编排层) ★ 新增多 hop 编排                      │   │
│  │                                                                          │   │
│  │  1. 校验 UpgradePath → hopPath = ["v2.6.5", "v2.7.0"]                  │   │
│  │  2. 校验相邻 hop K8s 版本差 ≤ 1                                         │   │
│  │  3. orchestrateMinimalMultiHop():                                       │   │
│  │     │                                                                   │   │
│  │     ├─ Hop 1 (v2.6.5, K8s v1.35):                                      │   │
│  │     │   ├── 设置 upgrade-ready = "v2.6.5" 注解                          │   │
│  │     │   ├── 设置 skip-kubelet = "true" 注解 ★ 新增                      │   │
│  │     │   ├── 等待 BKEClusterReconciler 执行 DAG 完成                     │   │
│  │     │   │   └── DAG 执行 kubernetes-master inline handler:              │   │
│  │     │   │       └── EnsureMasterUpgrade:                                │   │
│  │     │   │           ├── 读取 skip-kubelet 注解 → 跳过 kubelet 升级     │   │
│  │     │   │           ├── kubeadm upgrade apply (仅 apiserver/cm/scheduler)│  │
│  │     │   │           └── 不执行 installKubeletCommand                    │   │
│  │     │   ├── 解析 apiserver 版本 (从 ReleaseImage)                       │   │
│  │     │   └── 偏差门控: kubelet(v1.34) vs apiserver(v1.35) → 1 → ✅      │   │
│  │     │                                                                   │   │
│  │     ├─ Hop 2 (v2.7.0, K8s v1.36):                                      │   │
│  │     │   ├── 设置 upgrade-ready = "v2.7.0" 注解                          │   │
│  │     │   ├── 设置 skip-kubelet = "true" 注解                             │   │
│  │     │   ├── 等待 BKEClusterReconciler 执行 DAG 完成                     │   │
│  │     │   └── 偏差门控: kubelet(v1.34) vs apiserver(v1.36) → 2 → ✅      │   │
│  │     │                                                                   │   │
│  │     └─ Kubelet 补充升级 (最后一个 hop 完成后):                          │   │
│  │         ├── 设置 skip-kubelet = "false" 注解                            │   │
│  │         ├── 设置 upgrade-ready = "v2.7.0" 注解 (重新触发 worker 升级)  │   │
│  │         ├── 等待 BKEClusterReconciler 执行 worker 升级                  │   │
│  │         │   └── EnsureWorkerUpgrade:                                    │   │
│  │         │       └── kubeadm upgrade node (仅 kubelet + kubectl)        │   │
│  │         └── 最终偏差验证: kubelet(v1.36) vs apiserver(v1.36) → 0 → ✅  │   │
│  └──────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌──────────────────────────────────────────────────────────────────────────┐   │
│  │  BKEClusterReconciler (执行层) — 现有逻辑，仅 EnsureMasterUpgrade 微调   │   │
│  │                                                                          │   │
│  │  executeUpgradeDAG():                                                    │   │
│  │  ├── 读取 upgrade-ready 注解 → hopTarget                                │   │
│  │  ├── 读取 skip-kubelet 注解 → skipKubeletUpgrade ★ 新增                  │   │
│  │  ├── 构建 VersionContext                                                 │   │
│  │  │   └── 如果 skipKubeletUpgrade:                                       │   │
│  │  │       vc.SetTarget("kubelet", vc.GetCurrent("kubelet"))  ← 跳过      │   │
│  │  ├── 构建 DAG (不变: kubernetes-master 仍是 inline 节点)                │   │
│  │  └── Scheduler.ExecuteDAG()                                              │   │
│  │      └── InlineComponentExecutor → EnsureMasterUpgrade.Execute()         │   │
│  │          └── 如果 skipKubeletUpgrade:                                    │   │
│  │              ├── kubeadm upgrade apply --skip-kubelet ★ 新增参数         │   │
│  │              └── 不执行 installKubeletCommand                            │   │
│  └──────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 修改清单

| 文件 | 修改类型 | 修改内容 | 工作量 |
|------|---------|---------|--------|
| `controllers/clusterversion/clusterversion_controller.go` | **修改** | 新增 `orchestrateMinimalMultiHop()` 方法 + 偏差门控逻辑 | 4 人日 |
| `pkg/phaseframe/phases/ensure_master_upgrade.go` | **修改** | 新增 `skipKubeletUpgrade` 参数读取 + kubeadm 命令参数透传 | 2 人日 |
| `pkg/job/builtin/kubeadm/kubeadm.go` | **修改** | `upgradeControlPlane()` 新增 `skipKubelet` 参数 + 条件跳过 `installKubeletCommand` | 2 人日 |
| `pkg/upgrade/skew_checker.go` | **新增** | 偏差检查器 (复用 KEP-14 设计，仅检查 kubelet vs apiserver) | 2 人日 |
| `pkg/annotation/annotations.go` | **修改** | 新增 `SkipKubeletUpgradeAnnotationKey` 注解常量 | 0.5 人日 |
| 集成测试 | **新增** | 2-hop 升级 + kubelet 延迟 + 偏差门控 + 补充升级 E2E | 1.5 人日 |
| **合计** | | | **12 人日** |

---

## 5. 详细设计

### 5.1 编排层：ClusterVersionReconciler 多 hop 编排

#### 5.1.1 核心方法

```go
// controllers/clusterversion/clusterversion_controller.go

// orchestrateMinimalMultiHop 最小化多 hop 编排
// 保留 kubernetes-master inline 不变，通过注解控制 kubelet 跳过
func (r *ClusterVersionReconciler) orchestrateMinimalMultiHop(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopPath []string, // ["v2.6.5", "v2.7.0"] (openFuyao 版本)
) error {
    skewChecker := &upgrade.VersionSkewChecker{}

    // kubelet 当前版本 (从 Node 资源读取或 BKECluster.Status)
    kubeletCurrentVersion := r.getKubeletVersion(bkeCluster)

    for i, hopTarget := range hopPath {
        log.Info("starting hop", "hop", i+1, "target", hopTarget,
            "kubeletCurrent", kubeletCurrentVersion)

        // 1. 设置注解: upgrade-ready + skip-kubelet
        //    skip-kubelet=true → EnsureMasterUpgrade 跳过 kubelet 升级
        annotations := map[string]string{
            annotation.UpgradeReadyAnnotationKey:       hopTarget,
            annotation.SkipKubeletUpgradeAnnotationKey: "true", // ★ 新增
        }
        if err := r.setClusterAnnotations(ctx, bkeCluster, annotations); err != nil {
            return err
        }

        // 2. 等待 BKEClusterReconciler 执行 DAG 完成
        //    BKEClusterReconciler 读取 upgrade-ready 注解 → 执行 DAG
        //    DAG 中 kubernetes-master inline handler 读取 skip-kubelet 注解 → 跳过 kubelet
        if err := r.waitForHopCompletion(ctx, bkeCluster, hopTarget); err != nil {
            return fmt.Errorf("hop %d (%s) failed: %w", i+1, hopTarget, err)
        }

        // 3. 解析当前 hop 的 apiserver 版本 (从 ReleaseImage)
        apiserverVersion := r.resolveApiserverVersion(ctx, hopTarget)

        // 4. 偏差门控
        currentSkew := upgrade.ComputeMinorVersionSkew(apiserverVersion, kubeletCurrentVersion)
        log.Info("hop completed, checking skew",
            "hop", i+1, "apiserver", apiserverVersion,
            "kubelet", kubeletCurrentVersion, "skew", currentSkew, "maxSkew", 3)

        if i < len(hopPath)-1 {
            // 还有下一个 hop，前瞻性检查
            nextApiserverVersion := r.resolveApiserverVersion(ctx, hopPath[i+1])
            nextSkew := upgrade.ComputeMinorVersionSkew(nextApiserverVersion, kubeletCurrentVersion)

            if nextSkew >= 3 {
                // 下一个 hop 后偏差将达到极限，必须先升级 kubelet
                log.Info("skew will exceed limit after next hop, must upgrade kubelet now",
                    "currentSkew", currentSkew, "projectedNextSkew", nextSkew, "maxSkew", 3)

                if err := r.upgradeKubeletCatchup(ctx, bkeCluster, kubeletCurrentVersion, apiserverVersion); err != nil {
                    return fmt.Errorf("kubelet catchup failed: %w", err)
                }
                kubeletCurrentVersion = apiserverVersion
            }
        } else {
            // 最后一个 hop，必须将 kubelet 升级到最终目标版本
            log.Info("final hop completed, upgrading kubelet to final target version",
                "target", apiserverVersion)

            if err := r.upgradeKubeletCatchup(ctx, bkeCluster, kubeletCurrentVersion, apiserverVersion); err != nil {
                return fmt.Errorf("final kubelet catchup failed: %w", err)
            }
            kubeletCurrentVersion = apiserverVersion
        }

        log.Info("hop fully completed",
            "hop", i+1, "version", hopTarget,
            "apiserver", apiserverVersion, "kubelet", kubeletCurrentVersion)
    }

    return nil
}
```

#### 5.1.2 kubelet 补充升级

```go
// upgradeKubeletCatchup kubelet 补充升级
// 复用现有 EnsureWorkerUpgrade 机制，通过注解驱动 worker 升级
func (r *ClusterVersionReconciler) upgradeKubeletCatchup(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    currentVersion string,
    targetVersion string,
) error {
    log.Info("starting kubelet catchup",
        "from", currentVersion, "to", targetVersion)

    // 计算中间版本 (kubeadm 不允许跳小版本)
    intermediateVersions := upgrade.ComputeIntermediateVersions(currentVersion, targetVersion)
    // 如: v1.34.0 → v1.36.0 → ["v1.35.0", "v1.36.0"]

    for _, version := range intermediateVersions {
        log.Info("upgrading kubelet to intermediate version", "target", version)

        // 设置注解: 触发 worker 升级 (kubelet only)
        // skip-kubelet=false → EnsureWorkerUpgrade 正常执行 kubelet 升级
        annotations := map[string]string{
            annotation.UpgradeReadyAnnotationKey:            bkeCluster.Status.OpenFuyaoVersion, // 当前 hop 版本
            annotation.SkipKubeletUpgradeAnnotationKey:      "false",
            annotation.KubeletCatchupTargetAnnotationKey:     version, // ★ 新增: 指定 kubelet 目标版本
        }
        if err := r.setClusterAnnotations(ctx, bkeCluster, annotations); err != nil {
            return err
        }

        // 等待 worker 升级完成
        if err := r.waitForKubeletUpgradeCompletion(ctx, bkeCluster, version); err != nil {
            return fmt.Errorf("kubelet upgrade to %s failed: %w", version, err)
        }

        log.Info("kubelet upgraded to intermediate version", "version", version)
    }

    return nil
}
```

#### 5.1.3 辅助方法

```go
// resolveApiserverVersion 从 ReleaseImage 解析 apiserver 版本
// 由于 kubernetes-master 仍是 inline，K8s 版本从 ReleaseImage 的 kubernetesVersion 或
// BKECluster.Status.KubernetesVersion 读取
func (r *ClusterVersionReconciler) resolveApiserverVersion(
    ctx context.Context,
    hopTarget string,
) string {
    // 从 ReleaseImage bundle 解析 K8s 版本
    bundle, err := r.resolveReleaseBundle(ctx, hopTarget)
    if err != nil {
        return ""
    }

    // kubernetes-master 的 version 即为 K8s 版本 (apiserver/cm/scheduler 共用)
    for _, comp := range bundle.Release.Spec.Upgrade.Components {
        if comp.Name == "kubernetes-master" || comp.Name == "kubernetes-worker" {
            return comp.Version
        }
    }
    return ""
}

// getKubeletVersion 获取当前 kubelet 版本
// 优先从 BKECluster.Status 读取，回退到 Node 资源
func (r *ClusterVersionReconciler) getKubeletVersion(bkeCluster *bkev1beta1.BKECluster) string {
    // 优先从 BKECluster.Status.KubeletVersion (如果存在)
    // 回退从第一个 Node 的 NodeInfo.KubeletVersion 读取
    // 或从 BKECluster.Status.KubernetesVersion (当前与 kubelet 一致)
    return bkeCluster.Status.KubernetesVersion
}

// waitForHopCompletion 等待 BKEClusterReconciler 完成 DAG 执行
// 通过轮询 BKECluster.Status.DeclarativeUpgrade 或 ClusterVersion.Phase 判断完成
func (r *ClusterVersionReconciler) waitForHopCompletion(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    hopTarget string,
) error {
    // 轮询 ClusterVersion.Status.Phase
    // Upgraded → hop 完成
    // Failed → hop 失败
    // 超时 → 30 分钟
    pollInterval := 5 * time.Second
    timeout := 30 * time.Minute

    return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true,
        func(ctx context.Context) (bool, error) {
            cv, err := r.getClusterVersion(ctx, bkeCluster)
            if err != nil {
                return false, err
            }
            if cv.Status.Phase == "Upgraded" || cv.Status.Phase == "Ready" {
                return true, nil
            }
            if cv.Status.Phase == "Failed" {
                return false, fmt.Errorf("hop %s failed", hopTarget)
            }
            return false, nil // 继续等待
        })
}
```

### 5.2 执行层：EnsureMasterUpgrade 微调

#### 5.2.1 读取 skip-kubelet 注解

```go
// pkg/phaseframe/phases/ensure_master_upgrade.go

func (e *EnsureMasterUpgrade) Execute() (ctrl.Result, error) {
    bkeCluster := e.Ctx.BKECluster

    // ★ 新增: 读取 skip-kubelet 注解
    skipKubelet := annotation.GetAnnotation(bkeCluster,
        annotation.SkipKubeletUpgradeAnnotationKey) == "true"

    if skipKubelet {
        e.Ctx.Log.Info("skipKubeletUpgrade is set, will skip kubelet binary upgrade",
            "targetVersion", e.desiredKubernetesVersion())
    }

    // 设置 DeployAction annotation (不变)
    annotation.SetAnnotation(bkeCluster,
        annotation.DeployActionAnnotationKey,
        annotation.DeployActionK8sUpgrade)

    return e.reconcileMasterUpgrade(skipKubelet) // ★ 传递参数
}
```

#### 5.2.2 透传 skipKubelet 到 kubeadm 命令

```go
// pkg/phaseframe/phases/ensure_master_upgrade.go

func (e *EnsureMasterUpgrade) reconcileMasterUpgrade(skipKubelet bool) (ctrl.Result, error) {
    targetVersion := e.desiredKubernetesVersion()
    currentVersion := e.currentKubernetesVersion()

    if targetVersion != currentVersion {
        e.syncLegacyTargetKubernetesVersion(targetVersion)
        return e.rolloutUpgrade(skipKubelet) // ★ 传递参数
    }

    e.Ctx.Log.Info("k8s version same, not need to upgrade")
    return ctrl.Result{}, nil
}

func (e *EnsureMasterUpgrade) rolloutUpgrade(skipKubelet bool) (ctrl.Result, error) {
    // ... 获取节点、确定 etcd 备份节点 (不变) ...

    // ★ 修改: 传递 skipKubelet 到节点升级参数
    return e.upgradeMasterNodesWithParams(skipKubelet)
}

func (e *EnsureMasterUpgrade) upgradeMasterNodesWithParams(skipKubelet bool) (ctrl.Result, error) {
    // ... 获取需要升级的 Master 节点 (不变) ...

    for _, node := range masterNodes {
        // ... 跳过已是目标版本的节点 (不变) ...

        // ★ 修改: 创建 Upgrade Command 时传递 skipKubelet
        masterParams := CreateUpgradeCommandParams{
            // ... 现有参数不变 ...
            Phase:         bkev1beta1.UpgradeControlPlane,
            SkipKubelet:   skipKubelet, // ★ 新增
        }

        upgrade := createUpgradeCommand(masterParams)
        // ... 创建 Command CR + 等待完成 (不变) ...
    }

    // ... 更新集群版本 (不变) ...
}
```

### 5.3 BKEAgent 层：kubeadm 命令微调

#### 5.3.1 版本传递问题

基于 §7.7 的代码分析，BKEAgent 渲染 Static Pod manifest 时的镜像 tag 和 kubelet/kubectl 二进制版本**全部来自 `BkeConfig.Cluster.KubernetesVersion` 和 `BkeConfig.Cluster.EtcdVersion`** (即 `BKECluster.Spec.ClusterConfig.Cluster` 的字段)。BKEAgent 通过 `getBKEConfig()` 从管理集群读取 BKECluster CR 获取这些值。

因此，最小化方案中每个 hop 需要确保：**在 BKEClusterReconciler 执行 DAG 之前，`BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion` 和 `EtcdVersion` 已被正确设置为当前 hop 对应 ReleaseImage 中的版本值**。

现有代码已通过 `SyncUpgradeTargetsToClusterSpec()` 实现了这一同步 (见 §7.3.4)，但需要验证该同步在多 hop 场景下是否正确工作，并补充 etcd 版本的同步路径。

#### 5.3.2 每 hop 版本设置完整链路

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              每 hop 版本设置链路 (etcd + K8s 核心组件)                            │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ClusterVersionReconciler.orchestrateMinimalMultiHop():                         │
│                                                                                 │
│  for each hopTarget (openFuyao 版本, 如 "v2.6.5"):                              │
│                                                                                 │
│    ① 设置 upgrade-ready = hopTarget 注解                                        │
│    ② 设置 skip-kubelet = "true" 注解                                             │
│                                                                                 │
│    ③ BKEClusterReconciler.executeUpgradeDAG() 被触发:                            │
│       │                                                                         │
│       ├── resolveUpgradeBundle(hopTarget)                                        │
│       │   → 通过 openFuyao 版本查找 ReleaseImage CR (Spec.Version == hopTarget)  │
│       │   → 解析 OCI Bundle                                                      │
│       │   → bundle.Components["kubernetes-master"].Spec.Version = "v1.35.0-of.1"│
│       │   → bundle.Components["etcd"].Spec.Version = "v3.6.7-of.1"            │
│       │                                                                         │
│       ├── BuildVersionContextForUpgrade(bundle, currentBundle, bc):             │
│       │   VC.Target["kubernetes-master"] = "v1.35.0-of.1"  (K8s 版本)          │
│       │   VC.Target["etcd"] = "v3.6.7-of.1"                (etcd 版本)        │
│       │   VC.Target["kubernetes-worker"] = "v1.35.0-of.1"  (K8s 版本)          │
│       │                                                                         │
│       ├── SyncUpgradeTargetsToClusterSpec()  ★ 关键: 将 VC Target 同步到 Spec   │
│       │   ApplyVersionContextTargetsToClusterSpec(bc, vc):                      │
│       │     cluster.EtcdVersion = vc.GetTarget("etcd")     = "v3.6.7-of.1"   │
│       │     cluster.KubernetesVersion = KubernetesTargetFromVersionContext(vc)  │
│       │                                = "v1.35.0-of.1"                        │
│       │   → 通过 mergecluster.SyncStatusUntilComplete API patch 持久化          │
│       │                                                                         │
│       ├── patchClusterOpenFuyaoVersionSpecBeforeDAG(hopTarget)                   │
│       │   cluster.OpenFuyaoVersion = "v2.6.5" (openFuyao 版本)               │
│       │                                                                         │
│       ├── ★ skipKubelet=true 时:                                                 │
│       │   VC.SetTarget("kubernetes-worker", VC.GetCurrent("kubernetes-worker"))  │
│       │   → kubernetes-worker Target = Current (跳过 worker 升级)              │
│       │   注意: KubernetesVersion 已在 Spec 中 (步骤 SyncUpgradeTargets)        │
│       │   但 VC 中 kubernetes-worker Target 被设为 Current → NeedsUpgrade=false │
│       │   → EnsureWorkerUpgrade 跳过 → kubelet 不升级                            │
│       │   ★ 但 BKECluster.Spec.KubernetesVersion 仍是 "v1.35.0-of.1"          │
│       │   → 如果 EnsureMasterUpgrade 不跳过 installKubeletCommand,             │
│       │     kubelet 会被升级到 v1.35.0-of.1 (因为 BkeConfig 读取 Spec)          │
│       │   → 所以 skipKubelet 必须在 EnsureMasterUpgrade 中单独控制 (见 §5.3.3) │
│       │                                                                         │
│       └── ExecuteDAG → EnsureMasterUpgrade.Execute():                            │
│           EnsureMasterUpgrade 通过 Command CR 触发 BKEAgent                      │
│           BKEAgent getBKEConfig() → 读取 BKECluster.Spec.ClusterConfig           │
│           → BkeConfig.Cluster.KubernetesVersion = "v1.35.0-of.1"              │
│           → BkeConfig.Cluster.EtcdVersion = "v3.6.7-of.1"                    │
│                                                                                 │
│    ④ BKEAgent 执行:                                                              │
│       EnsureEtcdUpgrade (独立 Phase, 在 EnsureMasterUpgrade 之前):              │
│       → etcd manifest 渲染: image tag = EtcdVersion 去 v = "3.6.7-of.1"        │
│       → 镜像: cr.openfuyao.cn/openfuyao/etcd:3.6.7-of.1                        │
│                                                                                 │
│       EnsureMasterUpgrade → upgradeControlPlane(skipKubelet=true):              │
│       → apiserver manifest: image tag = KubernetesVersion 去 v = "1.35.0-of.1" │
│       → 镜像: cr.openfuyao.cn/openfuyao/kube-apiserver:1.35.0-of.1             │
│       → cm manifest: cr.openfuyao.cn/openfuyao/kube-controller-manager:1.35.0-of.1│
│       → scheduler manifest: cr.openfuyao.cn/openfuyao/kube-scheduler:1.35.0-of.1│
│       → 跳过 installKubeletCommand (skipKubelet=true)                           │
│       → installKubectlCommand: kubectl-v1.35.0-of.1-amd64 (从 HTTPRepo 下载)   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 5.3.3 skipKubelet 的双重控制

基于 §7.7.4 的分析，kubelet 二进制升级有**两个执行路径**，都需要控制：

| 执行路径 | 触发条件 | 版本来源 | 控制方式 |
|---------|---------|---------|---------|
| `EnsureMasterUpgrade` → `installKubeletCommand()` | 控制面升级时 (UpgradeControlPlane Phase) | `BkeConfig.Cluster.KubernetesVersion` | ★ `skipKubelet` 参数跳过 (§5.3.4) |
| `EnsureWorkerUpgrade` → `installKubeletCommand()` | Worker 升级时 (UpgradeWorker Phase) | `BkeConfig.Cluster.KubernetesVersion` | ★ VC 设 `kubernetes-worker` Target=Current 跳过 (已有机制) |

**问题**：即使 VC 将 `kubernetes-worker` Target 设为 Current 跳过了 `EnsureWorkerUpgrade`，`EnsureMasterUpgrade` 内部的 `installKubeletCommand()` 仍会执行 (因为它读取的是 `BkeConfig.Cluster.KubernetesVersion`，而非 VC)。

**解决**：`skipKubelet` 参数仅在 `EnsureMasterUpgrade` → `upgradeControlPlane()` 中控制 `installKubeletCommand()` 的跳过，确保 kubelet 二进制不被替换。

```go
// pkg/job/builtin/kubeadm/kubeadm.go

func (k *KubeadmPlugin) upgradeControlPlane(backUpEtcd bool, clusterType string, skipKubelet bool) error {
    // step 1: prepareUpgrade (不变)
    // 备份 etcd + 备份 /etc/kubernetes + 预拉镜像 + 获取 Pod Hash
    beforeHash, err := k.prepareUpgrade(backUpEtcd, clusterType)

    // step 2: 逐个升级控制面组件 (不变: apiserver → cm → scheduler)
    // 渲染 manifest: image tag 来自 BkeConfig.Cluster.KubernetesVersion (已由 SyncUpgradeTargets 同步)
    for _, component := range mfutil.GetControlPlaneComponents() {
        k.upgradeControlPlaneManifestCommand(component)  // 渲染 Go 模板写入 manifest
        k.waitComponentReady(component, podHash)          // 等待 Pod Running
    }

    // step 3: ★ 条件跳过 kubelet 二进制升级
    // installKubeletCommand 从 BkeConfig.Cluster.KubernetesVersion 读取版本
    // skipKubelet=true → 跳过，kubelet 保持旧版本
    if !skipKubelet {
        log.Infof("upgrade kubelet for cluster %s", k.clusterName)
        if err := k.installKubeletCommand(false); err != nil {
            return err
        }
    } else {
        log.Infof("skip kubelet upgrade (deferred) for cluster %s", k.clusterName)
        // kubelet 二进制不替换，保持旧版本
        // apiserver/cm/scheduler manifest 已替换为新版本 (step 2)
        // Kubelet (旧版本) 仍能管理新版本 apiserver/cm/scheduler Pod
    }

    // step 4: 安装 kubectl (不变)
    // installKubectlCommand 从 BkeConfig.Cluster.KubernetesVersion 读取版本
    // kubectl 偏差窗口仅 ±1，必须随控制面同步升级
    k.installKubectlCommand()

    return nil
}
```

#### 5.3.4 etcd 版本的传递

etcd 版本通过 `EnsureEtcdUpgrade` Phase 独立升级 (在 `EnsureMasterUpgrade` 之前)。etcd 的 image tag 解析有独立优先级链 (见 §7.7.5)，但最终来源仍是 `BkeConfig.Cluster.EtcdVersion`，由 `SyncUpgradeTargetsToClusterSpec()` 从 VC 同步。

```go
// SyncUpgradeTargetsToClusterSpec 中的 etcd 版本同步 (已有代码，无需修改)

func ApplyVersionContextTargetsToClusterSpec(bc *bkev1beta1.BKECluster, vc *upgrade.VersionContext) {
    cluster := &bc.Spec.ClusterConfig.Cluster
    if v := vc.GetTarget(ComponentEtcd); v != "" {
        cluster.EtcdVersion = v  // ← etcd 版本从 VC 写入 Spec
    }
    if v := vc.GetTarget(ComponentContainerd); v != "" {
        cluster.ContainerdVersion = v
    }
    if v := KubernetesTargetFromVersionContext(vc); v != "" {
        cluster.KubernetesVersion = v  // ← K8s 版本从 VC 写入 Spec
    }
}
```

BKEAgent 端 etcd manifest 渲染：

```go
// etcd 的 imageInfo 模板函数
"imageInfo": func(cfg *BootScope) string {
    etcdVersion := etcdImageTagFromBootScope(cfg)
    return fmt.Sprintf("%s:%s", bkeinit.DefaultEtcdImageName, etcdVersion)
    // → "etcd:3.6.7-of.1"
},

// etcdImageTagFromBootScope 优先级:
// 1. cfg.Extra["etcdVersion"] (声明式升级路径, 由 applyCommandEtcdVersion 设置)
// 2. cfg.BkeConfig.Cluster.EtcdVersion (从 BKECluster.Spec 读取)
// 3. versions.EtcdImageTag() (内嵌默认)
```

> etcd 版本传递**不需要额外修改** — `SyncUpgradeTargetsToClusterSpec` 已将 `VC.Target["etcd"]` (来源于 ReleaseImage `etcd.version`) 同步到 `BKECluster.Spec.EtcdVersion`，BKEAgent 读取后渲染 manifest。

#### 5.3.5 每 hop 版本设置汇总

| 组件 | 版本来源 (管理集群) | 同步路径 | BKEAgent 读取位置 | 渲染产物 |
|------|-------------------|---------|------------------|---------|
| kube-apiserver | ReleaseImage `kubernetes-master.version` | VC → `SyncUpgradeTargets` → `Spec.KubernetesVersion` | `BkeConfig.Cluster.KubernetesVersion` | manifest image tag |
| kube-controller-manager | ReleaseImage `kubernetes-master.version` | 同上 | 同上 | manifest image tag |
| kube-scheduler | ReleaseImage `kubernetes-master.version` | 同上 | 同上 | manifest image tag |
| etcd | ReleaseImage `etcd.version` | VC → `SyncUpgradeTargets` → `Spec.EtcdVersion` | `BkeConfig.Cluster.EtcdVersion` (或 `Extra["etcdVersion"]`) | manifest image tag |
| kubelet | ReleaseImage `kubernetes-worker.version` | VC → `SyncUpgradeTargets` → `Spec.KubernetesVersion` | `BkeConfig.Cluster.KubernetesVersion` | 二进制下载 URL |
| kubectl | ReleaseImage `kubernetes-master.version` | 同 kubelet | 同上 | 二进制下载 URL |

> **关键**：所有 K8s 核心组件 (apiserver/cm/scheduler/kubelet/kubectl) 的版本**都来自同一个字段** `BkeConfig.Cluster.KubernetesVersion`。etcd 来自独立字段 `BkeConfig.Cluster.EtcdVersion`。两个字段在 DAG 执行前由 `SyncUpgradeTargetsToClusterSpec()` 从 VersionContext (来源于 ReleaseImage) 同步。

#### 5.3.6 skipKubelet 时 Spec.KubernetesVersion 的影响分析

| 场景 | `Spec.KubernetesVersion` | `skipKubelet` | 影响 |
|------|--------------------------|--------------|------|
| 正常升级 (不跳过) | v1.35.0-of.1 (SyncUpgradeTargets 写入) | false | apiserver/cm/scheduler manifest → v1.35 + kubelet 二进制 → v1.35 |
| 延迟升级 (跳过 kubelet) | v1.35.0-of.1 (SyncUpgradeTargets 写入) | true | apiserver/cm/scheduler manifest → v1.35 + **kubelet 不升级** (保持 v1.34) |
| kubelet 补充升级 | v1.35.0-of.1 (catchup-target 注解覆盖) | false | kubelet 二进制 → v1.35 (仅 worker 升级) |

> **注意**：延迟升级时 `Spec.KubernetesVersion` 仍被设为 v1.35.0-of.1 (由 `SyncUpgradeTargets` 写入)。这意味着 BKEAgent 读取到的 `BkeConfig.Cluster.KubernetesVersion` 是 v1.35.0-of.1。如果不跳过 `installKubeletCommand()`，kubelet 会被升级到 v1.35.0-of.1。`skipKubelet=true` 仅控制 `installKubeletCommand()` 的跳过，不影响 manifest 渲染 (apiserver/cm/scheduler 仍按 v1.35.0-of.1 渲染)。

### 5.4 新增注解常量

```go
// pkg/annotation/annotations.go

const (
    // SkipKubeletUpgradeAnnotationKey 控制 kubeadm 跳过 kubelet 升级
    // "true" → 跳过 kubelet 二进制升级 (延迟升级)
    // "false" 或不存在 → 正常升级 kubelet
    SkipKubeletUpgradeAnnotationKey = "cvo.openfuyao.cn/skip-kubelet"

    // KubeletCatchupTargetAnnotationKey 指定 kubelet 补充升级的目标版本
    // 用于 kubelet 补充升级阶段，指定逐版本升级的中间目标
    KubeletCatchupTargetAnnotationKey = "cvo.openfuyao.cn/kubelet-catchup-target"
)
```

### 5.5 偏差检查器 (复用 KEP-14 设计，简化)

```go
// pkg/upgrade/skew_checker.go

// ComputeMinorVersionSkew 计算两个版本的小版本偏差
func ComputeMinorVersionSkew(reference, component string) int {
    refMinor := parseMinorVersion(reference)
    compMinor := parseMinorVersion(component)
    return refMinor - compMinor
}

// ComputeIntermediateVersions 计算中间版本列表
func ComputeIntermediateVersions(current, target string) []string {
    currentMinor := parseMinorVersion(current)
    targetMinor := parseMinorVersion(target)

    var versions []string
    for minor := currentMinor + 1; minor <= targetMinor; minor++ {
        versions = append(versions, fmt.Sprintf("v1.%d.0", minor))
    }
    return versions
}

func parseMinorVersion(version string) int {
    v := strings.TrimPrefix(version, "v")
    parts := strings.Split(v, ".")
    if len(parts) < 2 {
        return 0
    }
    minor, _ := strconv.Atoi(parts[1])
    return minor
}
```

---

## 6. 完整升级流程示例

```
场景: openFuyao v2.6.0 (K8s v1.34) → v2.7.0 (K8s v1.36)，跨 2 个 K8s 小版本

步骤 1: UpgradePath 定义合法路径
  v2.6.0 → v2.6.5 → v2.7.0 (3 个 openFuyao 版本，相邻 K8s 版本差 ≤ 1)

步骤 2: ClusterVersionReconciler 解析 hopPath
  hopPath = ["v2.6.5", "v2.7.0"]
  校验: v2.6.5(K8s v1.35) vs v2.6.0(K8s v1.34) = 1 ✅
  校验: v2.7.0(K8s v1.36) vs v2.6.5(K8s v1.35) = 1 ✅

步骤 3: Hop 1 (v2.6.5, K8s v1.35)
  ├── 设置注解: upgrade-ready=v2.6.5, skip-kubelet=true
  ├── BKEClusterReconciler 执行 DAG:
  │   ├── etcd: EnsureEtcdUpgrade → v3.5.19 (不变)
  │   └── kubernetes-master: EnsureMasterUpgrade
  │       ├── 读取 skip-kubelet=true
  │       ├── kubeadm upgrade apply v1.35 (apiserver/cm/scheduler manifest 替换)
  │       ├── 跳过 installKubeletCommand ★
  │       └── installKubectlCommand (kubectl 升级到 v1.35)
  ├── apiserver = v1.35, kubelet = v1.34 (保持不变)
  └── 偏差门控: kubelet(v1.34) vs apiserver(v1.35) → 1 → ✅ 安全

步骤 4: Hop 2 (v2.7.0, K8s v1.36)
  ├── 设置注解: upgrade-ready=v2.7.0, skip-kubelet=true
  ├── BKEClusterReconciler 执行 DAG:
  │   ├── etcd: EnsureEtcdUpgrade → v3.5.20 (不变)
  │   └── kubernetes-master: EnsureMasterUpgrade
  │       ├── 读取 skip-kubelet=true
  │       ├── kubeadm upgrade apply v1.36 (apiserver/cm/scheduler manifest 替换)
  │       ├── 跳过 installKubeletCommand ★
  │       └── installKubectlCommand (kubectl 升级到 v1.36)
  ├── apiserver = v1.36, kubelet = v1.34 (仍保持不变)
  └── 偏差门控: kubelet(v1.34) vs apiserver(v1.36) → 2 → ✅ 安全

步骤 5: Kubelet 补充升级 (最后一个 hop 完成后)
  ├── 中间版本: v1.34 → v1.35 → v1.36
  ├── Round 1 (v1.35):
  │   ├── 设置注解: skip-kubelet=false, kubelet-catchup-target=v1.35.0
  │   ├── 触发 EnsureWorkerUpgrade (kubelet only)
  │   │   └── kubeadm upgrade node (仅 kubelet 二进制替换 + 重启)
  │   └── 偏差: kubelet(v1.35) vs apiserver(v1.36) → 1 → ✅
  ├── Round 2 (v1.36):
  │   ├── 设置注解: kubelet-catchup-target=v1.36.0
  │   ├── 触发 EnsureWorkerUpgrade
  │   └── 偏差: kubelet(v1.36) vs apiserver(v1.36) → 0 → ✅
  └── 补充升级完成

步骤 6: 阶段二 — 其它组件升级 (现有 DAG 逻辑)
  ├── containerd → v1.7.24
  ├── bkeagent → v2.7.0
  └── ...

步骤 7: 升级完成
  ClusterVersion.Status.CurrentVersion = v2.7.0
```

---

## 7. kubernetes-master 版本信息来源与每 hop 设置分析

本节基于 `EnsureMasterUpgrade` 的实际代码实现，分析升级时 K8s 核心组件的版本信息从哪里获取、如何流转、每 hop 如何设置。

> **关键澄清**：ReleaseImage 中的 `kubernetes-master` 组件的 `version` 字段与 K8s 核心组件 (apiserver/cm/scheduler/kubelet) 的实际安装版本是**两个独立的概念**。ReleaseImage 中的 `version` 只是一个声明性目标值，实际安装到节点上的 K8s 组件版本由 kubeadm 根据下载的二进制/镜像决定。代码中的版本流转链路远比"直接读取 ReleaseImage 字段"复杂。

### 7.1 版本信息的完整流转链路

从用户触发升级到 kubeadm 执行升级命令，版本信息经过以下完整链路：

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              K8s 版本信息完整流转链路 (基于代码分析)                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ① 用户触发升级                                                                 │
│  ClusterVersion.Spec.DesiredVersion = "v2.7.0" (openFuyao 版本)                │
│     │                                                                           │
│     ▼                                                                           │
│  ② ClusterVersionReconciler 计算升级路径                                         │
│  resolveUpgradePath → hopPath = ["v2.6.5", "v2.7.0"] (openFuyao 版本)        │
│  设置 BKECluster 注解: cvo.openfuyao.cn/upgrade-ready = "v2.6.5"              │
│     │  (注解值是 openFuyao 版本，不是 K8s 版本)                                │
│     │                                                                           │
│     ▼                                                                           │
│  ③ BKEClusterReconciler.executeUpgradeDAG() 读取注解                             │
│  hopTarget = annotation["upgrade-ready"] = "v2.6.5" (openFuyao 版本)          │
│     │                                                                           │
│     ▼                                                                           │
│  ④ resolveUpgradeBundle(hopTarget) — 通过 openFuyao 版本解析 ReleaseImage      │
│  ResolveReleaseImageForVersion("v2.6.5")                                       │
│    → 查找 ReleaseImage CR where Spec.Version == "v2.6.5"                       │
│    → releaseStore().ResolveRelease(releaseRef) → OCI Bundle                     │
│     │                                                                           │
│     │  ReleaseImage CR 结构:                                                    │
│     │  spec.version = "v2.6.5"              ← openFuyao 版本                  │
│     │  spec.upgrade.components:                                               │
│     │    - name: kubernetes-master                                           │
│     │      version: "v1.35.0-of.1"       ← ★ K8s 版本 (独立于 openFuyao 版本) │
│     │      inline:                                                           │
│     │        handler: EnsureMasterUpgrade                                     │
│     │        version: "v1.0.0"          ← handler 代码版本 (独立于 K8s 版本)  │
│     │    - name: etcd                                                        │
│     │      version: "v3.5.19"           ← etcd 独立版本                       │
│     │    - name: kubernetes-worker                                           │
│     │      version: "v1.35.0-of.1"       ← K8s 版本                          │
│     │      inline:                                                           │
│     │        handler: EnsureWorkerUpgrade                                    │
│     │                                                                        │
│     ▼                                                                           │
│  ⑤ BuildVersionContextForUpgrade(targetBundle, currentBundle, bc)              │
│  applyReleaseComponents(SetTarget, targetBundle):                              │
│    遍历 ReleaseImage.upgrade.components + install.components                   │
│    vc.SetTarget("kubernetes-master", "v1.35.0-of.1")  ← 从组件 version 字段    │
│    vc.SetTarget("etcd", "v3.5.19")                                             │
│    vc.SetTarget("kubernetes-worker", "v1.35.0-of.1")                           │
│  applyReleaseComponents(SetCurrent, currentBundle) 或 fillCurrentFromBKECluster:│
│    vc.SetCurrent("kubernetes-master", bc.Status.KubernetesVersion)            │
│     │                                                                           │
│     ▼                                                                           │
│  ⑥ SyncUpgradeTargetsToClusterSpec() — 将 VC Target 同步到 BKECluster.Spec    │
│  ApplyVersionContextTargetsToClusterSpec(bc, vc):                              │
│    KubernetesTargetFromVersionContext(vc):                                     │
│      优先级: vc.GetTarget("kubernetes-master") → "v1.35.0-of.1"              │
│               → vc.GetTarget("kubernetes-worker")                             │
│               → vc.GetTarget("kubernetes")                                     │
│    → BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion = "v1.35.0-of.1" │
│    → BKECluster.Spec.ClusterConfig.Cluster.EtcdVersion = "v3.5.19"           │
│     │  (通过 mergecluster.SyncStatusUntilComplete API patch 持久化)            │
│     │                                                                           │
│     ▼                                                                           │
│  ⑦ patchClusterOpenFuyaoVersionSpecBeforeDAG(hopTarget) — 同步 openFuyao 版本  │
│  ApplyUpgradeHopToClusterSpec(bc, "v2.6.5"):                                   │
│    → BKECluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion = "v2.6.5"       │
│     │  (仅写 openFuyao 版本，不写 K8s 版本 — K8s 版本已由步骤⑥写入)           │
│     │                                                                           │
│     ▼                                                                           │
│  ⑧ DAG 执行 → InlineComponentExecutor → EnsureMasterUpgrade.Execute()          │
│  desiredKubernetesVersion() 解析 (优先级):                                      │
│    1. vc.GetTarget("kubernetes-master") → "v1.35.0-of.1"  ← ★ 主来源          │
│    2. vc.GetTarget("kubernetes-worker") → "v1.35.0-of.1"                       │
│    3. vc.GetTarget("kubernetes") → (如有)                                       │
│    4. BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion (deprecated 回退)│
│     │                                                                           │
│     │  ★ 此时 Spec 和 VC 中的 K8s 版本一致 (步骤⑥已同步)                      │
│     │     但 VC 是权威来源，Spec 仅为 legacy 代码路径的兼容                    │
│     │                                                                           │
│     ▼                                                                           │
│  ⑨ syncLegacyTargetKubernetesVersion(targetVersion) — 防御性 Spec 再同步       │
│  确保 BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion == targetVersion │
│     │  (供 upgradeMasterNodesWithParams / waitForNodeHealthCheck 等 legacy 读取)│
│     │                                                                           │
│     ▼                                                                           │
│  ⑩ kubeadm upgrade apply v1.35.0-of.1                                          │
│  BKEAgent 接收 Command CR → KubeadmPlugin.upgradeControlPlane()                │
│    → 实际下载 K8s 二进制/镜像 (版本由 ReleaseImage 中的镜像引用决定)           │
│    → 替换 apiserver/cm/scheduler manifest (镜像 tag 来自下载的制品)             │
│    → 实际安装的组件版本 = kubeadm 下载的制品版本 (可能与声明版本格式不同)       │
│                                                                                 │
│  ⑪ 升级完成后                                                                    │
│  bkeCluster.Status.KubernetesVersion = desiredKubernetesVersion()             │
│  completeDeclarativeUpgrade → ApplyUpgradeHopToClusterStatus:                 │
│    bc.Status.OpenFuyaoVersion = hopTarget (openFuyao 版本)                    │
│    bc.Status.KubernetesVersion = spec.KubernetesVersion (K8s 版本, 已同步)   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.2 openFuyao 版本与 K8s 版本的独立性

ReleaseImage 中存在**两个独立的版本轴**：

| 版本轴 | 示例 | 存储位置 | 用途 |
|--------|------|---------|------|
| **openFuyao 版本** (产品版本) | `v2.6.0`, `v2.7.0` | `ReleaseImage.Spec.Version`、`Cluster.OpenFuyaoVersion`、`ClusterVersion.Spec.DesiredVersion`、`upgrade-ready` 注解 | 标识产品发布版本，用于查找 ReleaseImage CR、驱动多 hop 升级路径 |
| **K8s 版本** (Kubernetes 版本) | `v1.35.0-of.1`, `v1.36.0` | `ReleaseImage.upgrade.components[kubernetes-master].version`、`Cluster.KubernetesVersion`、`Cluster.Status.KubernetesVersion` | 标识 Kubernetes 组件版本，用于 kubeadm 命令、偏差门控 |

**独立性说明**：

1. 一个 openFuyao 版本对应一个 K8s 版本，但两者数值不同 (如 `v2.6.5` → `v1.35.0-of.1`)
2. openFuyao 版本用于**查找 ReleaseImage** (通过 `Spec.Version` 匹配)
3. K8s 版本从 ReleaseImage 的**组件条目**中读取 (通过 `components[kubernetes-master].version`)
4. `kubernetes-master.version` 与 `kubernetes-master.inline.version` 也是独立的：前者是 K8s 版本，后者是 handler 代码版本

```yaml
# ReleaseImage v2.6.5 示例
apiVersion: cvo.openfuyao.cn/v1alpha1
kind: ReleaseImage
spec:
  version: "v2.6.5"                    # ← openFuyao 版本 (产品版本)
  upgrade:
    components:
      - name: kubernetes-master
        version: "v1.35.0-of.1"        # ← K8s 版本 (组件版本，独立于 openFuyao 版本)
        inline:
          handler: EnsureMasterUpgrade
          version: "v1.0.0"            # ← handler 代码版本 (独立于 K8s 版本)
      - name: kubernetes-worker
        version: "v1.35.0-of.1"        # ← K8s 版本 (与 kubernetes-master 一致)
        inline:
          handler: EnsureWorkerUpgrade
          version: "v1.0.0"
      - name: etcd
        version: "v3.5.19"             # ← etcd 独立版本 (非 K8s 版本)
```

### 7.3 EnsureMasterUpgrade 版本解析代码分析

#### 7.3.1 `desiredKubernetesVersion()` — 目标版本解析

```go
// pkg/phaseframe/phases/ensure_master_upgrade.go

func (e *EnsureMasterUpgrade) desiredKubernetesVersion() string {
    vc := e.GetVersionContext()
    if vc != nil {
        // 优先级 1: VersionContext 中 kubernetes-master 的 Target
        if target := strings.TrimSpace(vc.GetTarget(upgrade.ComponentKubernetesMaster)); target != "" {
            return target  // ← 来自 ReleaseImage kubernetes-master.version
        }
        // 优先级 2: VersionContext 中 kubernetes-worker 的 Target
        if target := strings.TrimSpace(vc.GetTarget(upgrade.ComponentKubernetesWorker)); target != "" {
            return target
        }
        // 优先级 3: VersionContext 中 kubernetes 的 Target
        if target := strings.TrimSpace(vc.GetTarget("kubernetes")); target != "" {
            return target
        }
    }
    // 优先级 4 (deprecated 回退): BKECluster.Spec
    return e.deprecatedSpecKubernetesVersion()
}
```

**版本来源链**：`VC.Target["kubernetes-master"]` ← `BuildVersionContextForUpgrade` ← `ReleaseImage.upgrade.components[kubernetes-master].version`

> **注意**：VC 是权威来源。`BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion` 仅作为 deprecated 回退，但实际在 DAG 执行前 `SyncUpgradeTargetsToClusterSpec()` 已将 VC Target 同步到 Spec，所以两者值一致。

#### 7.3.2 `currentKubernetesVersion()` — 当前版本解析

```go
func (e *EnsureMasterUpgrade) currentKubernetesVersion() string {
    vc := e.GetVersionContext()
    if vc != nil {
        // 优先级 1-3: VersionContext.Current (来自当前 ReleaseImage bundle 或 BKECluster.Status)
        if current := vc.GetCurrent(upgrade.ComponentKubernetesMaster); current != "" {
            return current
        }
        // ... kubernetes-worker, kubernetes 回退
    }
    // 优先级 4 (deprecated 回退): BKECluster.Status.KubernetesVersion
    return e.Ctx.BKECluster.Status.KubernetesVersion
}
```

**当前版本来源**：
- 如果有 `currentBundle` (当前版本的 ReleaseImage bundle)：从 bundle 中 `kubernetes-master.version` 解析
- 如果无 `currentBundle`：从 `BKECluster.Status.KubernetesVersion` 读取 (由 `clusterCurrentForReleaseComponent` 映射)

#### 7.3.3 `BuildVersionContextForUpgrade()` — VC 构建

```go
// pkg/upgrade/build_release.go

func BuildVersionContextForUpgrade(targetBundle, currentBundle *releasemanifest.Bundle,
    bc *bkev1beta1.BKECluster) *VersionContext {
    vc := NewVersionContext()

    // 填充 Target (从目标 ReleaseImage bundle)
    if targetBundle != nil {
        FillTargetFromBundle(vc, targetBundle)
    } else if bc != nil {
        // legacy 路径: 从 BKECluster.Spec 构建 (无 ReleaseImage 时)
        BuildVersionContextFromBKECluster(bc)
    }

    // 填充 Current (从当前 ReleaseImage bundle 或 BKECluster.Status)
    if currentBundle != nil {
        FillCurrentFromBundle(vc, currentBundle)
    } else {
        fillCurrentFromBKECluster(vc, bc)  // 从 Status 回退
    }
}

// FillTargetFromBundle 遍历 ReleaseImage 组件列表填充 VC.Target
func FillTargetFromBundle(vc *VersionContext, bundle *releasemanifest.Bundle) {
    applyReleaseComponents(vc.SetTarget, bundle)
}

// applyReleaseComponents 遍历 Install + Upgrade 组件
func applyReleaseComponents(set func(name, version string), bundle *releasemanifest.Bundle) {
    ri := bundle.Release
    // 先遍历 Install.Components
    if ri.Spec.Install != nil {
        for _, c := range ri.Spec.Install.Components {
            if c.Name != "" && c.Version != "" {
                set(c.Name, c.Version)  // ← vc.SetTarget("kubernetes-master", "v1.35.0-of.1")
            }
        }
    }
    // 再遍历 Upgrade.Components (覆盖 Install 中的同名组件)
    if ri.Spec.Upgrade != nil {
        for _, c := range ri.Spec.Upgrade.Components {
            if c.Name != "" && c.Version != "" {
                set(c.Name, c.Version)
            }
        }
    }
}
```

**关键点**：VC.Target 中 `kubernetes-master` 的值 = ReleaseImage 中 `kubernetes-master` 组件条目的 `version` 字段。这个值**不是** openFuyao 版本，**不是** handler 版本，而是 **K8s 版本** (如 `v1.35.0-of.1`)。

#### 7.3.4 `SyncUpgradeTargetsToClusterSpec()` — Spec 同步

```go
// pkg/phaseframe/spec_sync.go

func (pc *PhaseContext) SyncUpgradeTargetsToClusterSpec() error {
    vc := pc.VersionContext
    // 检查 Spec 是否已有 VC Target 值
    if !upgrade.ClusterSpecHasUpgradeTargets(pc.BKECluster.Spec.ClusterConfig.Cluster, vc) {
        // 无差异: 仅内存同步
        upgrade.ApplyVersionContextTargetsToClusterSpec(pc.BKECluster, vc)
        return nil
    }
    // 有差异: API patch 持久化
    return mergecluster.SyncStatusUntilComplete(pc.Client, pc.BKECluster, func(bc *bkev1beta1.BKECluster) {
        upgrade.ApplyVersionContextTargetsToClusterSpec(bc, vc)
    })
}

// pkg/upgrade/spec_sync.go

func ApplyVersionContextTargetsToClusterSpec(bc *bkev1beta1.BKECluster, vc *upgrade.VersionContext) {
    cluster := &bc.Spec.ClusterConfig.Cluster
    if v := vc.GetTarget(ComponentEtcd); v != "" {
        cluster.EtcdVersion = v
    }
    if v := vc.GetTarget(ComponentContainerd); v != "" {
        cluster.ContainerdVersion = v
    }
    // ★ K8s 版本从 VC 写入 Spec
    if v := KubernetesTargetFromVersionContext(vc); v != "" {
        cluster.KubernetesVersion = v  // ← "v1.35.0-of.1"
    }
}

func KubernetesTargetFromVersionContext(vc *upgrade.VersionContext) string {
    // 优先级: kubernetes-master → kubernetes-worker → kubernetes
    for _, name := range []string{
        ComponentKubernetesMaster,
        ComponentKubernetesWorker,
        releaseComponentKubernetes,
    } {
        if v := strings.TrimSpace(vc.GetTarget(name)); v != "" {
            return v
        }
    }
    return ""
}
```

**执行时机**：在 DAG 执行**之前** (步骤⑥)，确保 `EnsureMasterUpgrade.Execute()` 运行时 Spec 和 VC 中的 K8s 版本一致。

#### 7.3.5 `syncLegacyTargetKubernetesVersion()` — 防御性 Spec 再同步

```go
// pkg/phaseframe/phases/ensure_master_upgrade.go

func (e *EnsureMasterUpgrade) reconcileMasterUpgrade() (ctrl.Result, error) {
    targetVersion := e.desiredKubernetesVersion()    // 从 VC 读取
    currentVersion := e.currentKubernetesVersion()   // 从 VC 读取

    if targetVersion != "" && targetVersion != currentVersion {
        // ★ 防御性: 确保 Spec.KubernetesVersion == targetVersion
        if err := e.syncLegacyTargetKubernetesVersion(targetVersion); err != nil {
            return ctrl.Result{}, err
        }
        ret, err := e.rolloutUpgrade()
        // ...
    }
}

// syncLegacyTargetKubernetesVersion 确保 Spec 与 VC 一致
// 供 upgradeMasterNodesWithParams / waitForNodeHealthCheck 等 legacy 代码读取
func (e *EnsureMasterUpgrade) syncLegacyTargetKubernetesVersion(target string) error {
    if e.Ctx.BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion != target {
        // patch Spec
        return mergecluster.SyncStatusUntilComplete(...)
    }
    return nil
}
```

**为什么需要防御性同步**：`upgradeMasterNodesWithParams()` (line 333)、`updateAddonVersions()` (line 495)、`waitForNodeHealthCheckWithParams()` (line 641) 等 legacy 代码直接读取 `BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion`，而非读取 VC。防御性同步确保这些路径也能获得正确的 K8s 版本。

### 7.4 每 hop 升级时的版本设置

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              每 hop 的 K8s 版本设置 (基于代码分析)                                │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  当前状态: BKECluster.Status.KubernetesVersion = "v1.34.0-of.1"                │
│            BKECluster.Status.OpenFuyaoVersion = "v2.6.0"                       │
│            upgrade-ready 注解 = (未设置)                                        │
│                                                                                 │
│  hopPath = ["v2.6.5", "v2.7.0"] (openFuyao 版本)                              │
│                                                                                 │
│  ┌──────────────────────────────────────────────────────────────────────────┐   │
│  │ Hop 1 (hopTarget = "v2.6.5", openFuyao 版本)                             │   │
│  │                                                                          │   │
│  │ ClusterVersionReconciler:                                                │   │
│  │   设置注解: upgrade-ready = "v2.6.5" (openFuyao 版本)                    │   │
│  │   设置注解: skip-kubelet = "true"                                        │   │
│  │                                                                          │   │
│  │ BKEClusterReconciler.executeUpgradeDAG():                                │   │
│  │   ① hopTarget = "v2.6.5" (从注解读取 openFuyao 版本)                     │   │
│  │   ② resolveUpgradeBundle("v2.6.5"):                                      │   │
│  │      → 查找 ReleaseImage CR where Spec.Version == "v2.6.5"              │   │
│  │      → 解析 OCI Bundle                                                   │   │
│  │      → bundle.Components["kubernetes-master"].Spec.Version = "v1.35.0-of.1"│  │
│  │   ③ BuildVersionContextForUpgrade(bundle, currentBundle, bc):           │   │
│  │      VC.Target["kubernetes-master"] = "v1.35.0-of.1" (K8s 版本)         │   │
│  │      VC.Current["kubernetes-master"] = "v1.34.0-of.1" (当前 K8s 版本)   │   │
│  │   ④ SyncUpgradeTargetsToClusterSpec():                                   │   │
│  │      BKECluster.Spec.KubernetesVersion = "v1.35.0-of.1"                │   │
│  │   ⑤ patchClusterOpenFuyaoVersionSpecBeforeDAG("v2.6.5"):                │   │
│  │      BKECluster.Spec.OpenFuyaoVersion = "v2.6.5"                        │   │
│  │   ⑥ ★ skipKubelet=true:                                                 │   │
│  │      VC.SetTarget("kubernetes-worker", VC.GetCurrent("kubernetes-worker"))│  │
│  │      → Target["kubernetes-worker"] = "v1.34.0-of.1" (保持不变, 跳过)   │   │
│  │   ⑦ ExecuteDAG → EnsureMasterUpgrade.Execute():                          │   │
│  │      desiredKubernetesVersion() = VC.Target["kubernetes-master"]        │   │
│  │                                   = "v1.35.0-of.1" ← K8s 版本           │   │
│  │      currentKubernetesVersion() = VC.Current["kubernetes-master"]        │   │
│  │                                    = "v1.34.0-of.1"                      │   │
│  │      targetVersion != currentVersion → 执行升级                          │   │
│  │      kubeadm upgrade apply v1.35.0-of.1                                  │   │
│  │      (apiserver/cm/scheduler manifest → v1.35, kubelet 跳过)            │   │
│  │   ⑧ 升级完成后:                                                          │   │
│  │      BKECluster.Status.KubernetesVersion = "v1.35.0-of.1" (apiserver)   │   │
│  │      BKECluster.Status.OpenFuyaoVersion = "v2.6.5"                      │   │
│  │      kubelet = v1.34 (保持不变)                                         │   │
│  └──────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌──────────────────────────────────────────────────────────────────────────┐   │
│  │ Hop 2 (hopTarget = "v2.7.0", openFuyao 版本)                             │   │
│  │                                                                          │   │
│  │ ClusterVersionReconciler:                                                │   │
│  │   设置注解: upgrade-ready = "v2.7.0"                                     │   │
│  │   设置注解: skip-kubelet = "true"                                        │   │
│  │                                                                          │   │
│  │ BKEClusterReconciler.executeUpgradeDAG():                                │   │
│  │   ① hopTarget = "v2.7.0"                                                │   │
│  │   ② resolveUpgradeBundle("v2.7.0"):                                      │   │
│  │      → bundle.Components["kubernetes-master"].Spec.Version = "v1.36.0-of.1"│  │
│  │   ③ BuildVersionContextForUpgrade:                                       │   │
│  │      VC.Target["kubernetes-master"] = "v1.36.0-of.1"                    │   │
│  │      VC.Current["kubernetes-master"] = "v1.35.0-of.1" ← 从 Status 或 v2.6.5 bundle│
│  │   ④ SyncUpgradeTargetsToClusterSpec:                                     │   │
│  │      BKECluster.Spec.KubernetesVersion = "v1.36.0-of.1"                │   │
│  │   ⑤ patchClusterOpenFuyaoVersionSpecBeforeDAG("v2.7.0"):                 │   │
│  │      BKECluster.Spec.OpenFuyaoVersion = "v2.7.0"                        │   │
│  │   ⑥ ★ skipKubelet=true:                                                 │   │
│  │      VC.Target["kubernetes-worker"] = VC.Current["kubernetes-worker"]    │   │
│  │      → "v1.34.0-of.1" (保持不变)                                        │   │
│  │   ⑦ ExecuteDAG → EnsureMasterUpgrade:                                    │   │
│  │      kubeadm upgrade apply v1.36.0-of.1                                  │   │
│  │      (apiserver/cm/scheduler → v1.36, kubelet 跳过)                     │   │
│  │   ⑧ 升级完成后:                                                          │   │
│  │      BKECluster.Status.KubernetesVersion = "v1.36.0-of.1"              │   │
│  │      kubelet = v1.34 (仍保持不变, 偏差=2)                               │   │
│  └──────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
│  ┌──────────────────────────────────────────────────────────────────────────┐   │
│  │ Kubelet 补充升级                                                           │   │
│  │                                                                          │   │
│  │ ClusterVersionReconciler:                                                │   │
│  │   设置注解: skip-kubelet = "false"                                        │   │
│  │   设置注解: kubelet-catchup-target = "v1.35.0-of.1" (Round 1)            │   │
│  │                                                                          │   │
│  │ BKEClusterReconciler.executeUpgradeDAG():                                │   │
│  │   ① hopTarget = 当前 openFuyao 版本 (从 Status 读取)                      │   │
│  │   ② resolveUpgradeBundle(hopTarget) → 当前 ReleaseImage bundle           │   │
│  │   ③ BuildVersionContextForUpgrade:                                       │   │
│  │      VC.Target["kubernetes-master"] = "v1.36.0-of.1" (当前 ReleaseImage)│   │
│  │      VC.Target["kubernetes-worker"] = "v1.36.0-of.1" (当前 ReleaseImage)│   │
│  │   ⑥ ★ skipKubelet=false, 但有 catchup-target 注解:                       │   │
│  │      VC.SetTarget("kubernetes-worker", "v1.35.0-of.1")                   │   │
│  │      ← 从 kubelet-catchup-target 注解读取 (覆盖 ReleaseImage 中的版本)    │   │
│  │      VC.Target["kubernetes-master"] = "v1.34.0-of.1" (设为 Current, 跳过)│   │
│  │      ← kubernetes-master 不在补充升级范围                                │   │
│  │   ⑦ ExecuteDAG → EnsureWorkerUpgrade:                                    │   │
│  │      desiredKubernetesVersion() = kubelet-catchup-target = "v1.35.0-of.1"│   │
│  │      kubeadm upgrade node (kubelet → v1.35)                             │   │
│  │   ⑧ Round 2: kubelet-catchup-target = "v1.36.0-of.1"                     │   │
│  │      kubeadm upgrade node (kubelet → v1.36)                              │   │
│  └──────────────────────────────────────────────────────────────────────────┘   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.5 偏差门控时的版本来源

偏差门控需要获取 apiserver 版本和 kubelet 版本。由于 `kubernetes-master` 是 inline 黑盒，没有独立的 apiserver 字段，版本来源如下：

| 版本 | 来源 | 代码路径 | 说明 |
|------|------|---------|------|
| apiserver 版本 (升级后) | `BKECluster.Status.KubernetesVersion` | `EnsureMasterUpgrade` 执行完成后写入 | kubernetes-master 升级后 Status.KubernetesVersion = apiserver 版本 (因为 apiserver/cm/scheduler 共用 K8s 版本) |
| apiserver 版本 (hop 前) | `ReleaseImage.upgrade.components[kubernetes-master].version` | `resolveApiserverVersion()` 从 ReleaseImage 解析 | kubernetes-master 组件的 version 字段 = K8s 版本 |
| kubelet 版本 | `Node.NodeInfo.KubeletVersion` | 从目标集群 Node 资源读取 | 逐节点读取实际 kubelet 版本 |
| kubelet 版本 (回退) | `BKECluster.Status.KubernetesVersion` (升级前值) | `clusterCurrentForReleaseComponent` 映射 | 升级前 kubelet = apiserver = KubernetesVersion |

```go
// resolveApiserverVersion 从 ReleaseImage 解析 apiserver (K8s) 版本
// 注意: 不是直接读取 "apiserver 版本"，而是读取 kubernetes-master 组件的 version 字段
// (因为 kubernetes-master 是 inline 黑盒，apiserver/cm/scheduler 共用 K8s 版本)
func (r *ClusterVersionReconciler) resolveApiserverVersion(
    ctx context.Context,
    hopTarget string, // openFuyao 版本 (如 "v2.6.5")
) string {
    // ① 通过 openFuyao 版本查找 ReleaseImage
    bundle, err := r.resolveReleaseBundle(ctx, hopTarget)
    if err != nil {
        return ""
    }

    // ② 从 ReleaseImage 组件列表中查找 kubernetes-master 的 version
    //    这个 version 是 K8s 版本 (如 "v1.35.0-of.1")，不是 openFuyao 版本
    for _, comp := range bundle.Release.Spec.Upgrade.Components {
        if comp.Name == "kubernetes-master" {
            return comp.Version // = K8s 版本 = apiserver 版本
        }
    }
    // 回退: 从 Install.Components 查找
    for _, comp := range bundle.Release.Spec.Install.Components {
        if comp.Name == "kubernetes-master" {
            return comp.Version
        }
    }
    return ""
}
```

> **注意**：`resolveApiserverVersion()` 返回的是 ReleaseImage 中 `kubernetes-master` 组件的 `version` 字段值。这个值**声明**的是 K8s 版本 (如 `v1.35.0-of.1`)，但实际安装到节点上的组件版本由 kubeadm 下载的制品决定。在 ReleaseImage 构建正确的前提下，声明版本与实际安装版本一致。

### 7.6 版本信息流转总结

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              版本信息流转总结                                                     │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  两个独立版本轴:                                                                 │
│  ├── openFuyao 版本 (产品版本): v2.6.0, v2.6.5, v2.7.0                         │
│  │   用途: 查找 ReleaseImage CR, 驱动多 hop 路径, 写入 upgrade-ready 注解      │
│  │   来源: ClusterVersion.Spec.DesiredVersion → UpgradePath → hopPath          │
│  │                                                                             │
│  └── K8s 版本 (组件版本): v1.34.0-of.1, v1.35.0-of.1, v1.36.0-of.1           │
│      用途: kubeadm 命令参数, 偏差门控, 版本比较                                 │
│      来源: ReleaseImage.upgrade.components[kubernetes-master].version           │
│      → BuildVersionContextForUpgrade → VC.Target["kubernetes-master"]          │
│      → SyncUpgradeTargetsToClusterSpec → BKECluster.Spec.KubernetesVersion     │
│      → EnsureMasterUpgrade.desiredKubernetesVersion() → kubeadm upgrade apply │
│                                                                                 │
│  独立的 handler 版本:                                                             │
│  └── inline handler 版本: v1.0.0                                               │
│      用途: 标识 EnsureMasterUpgrade 代码版本 (与 K8s 版本无关)                 │
│      来源: ReleaseImage.upgrade.components[kubernetes-master].inline.version   │
│                                                                                 │
│  实际安装版本:                                                                    │
│  └── kubeadm 下载的制品版本 (由 ReleaseImage 中的镜像引用决定)                   │
│      用途: 节点上实际运行的组件版本 (apiserver/cm/scheduler/kubelet)            │
│      来源: ReleaseImage 中的 image-references → OCI 镜像 tag → 实际二进制版本  │
│      验证: Node.Status.NodeInfo.KubeletVersion (kubelet)                      │
│           BKECluster.Status.KubernetesVersion (apiserver, 升级后写入)          │
│                                                                                 │
│  声明版本 vs 实际版本:                                                            │
│  ├── ReleaseImage 声明 kubernetes-master.version = "v1.35.0-of.1" (声明)     │
│  ├── kubeadm 下载制品并安装 (实际)                                              │
│  └── BKECluster.Status.KubernetesVersion = "v1.35.0-of.1" (升级后写入)        │
│      → 在 ReleaseImage 构建正确的前提下，声明版本 = 实际版本                     │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 7.7 Static Pod manifest 镜像 tag 来源分析

#### 7.7.1 BKE 不调用 kubeadm 生成 manifest — 直接渲染 Go 模板

基于 `KubeadmPlugin.upgradeControlPlane()` 的实际代码分析，**BKE 不调用 `kubeadm` 命令来生成 Static Pod manifest，而是 BKE 自己渲染内嵌的 Go 模板直接写入 manifest YAML 文件**。

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              Static Pod manifest 生成机制 (基于代码分析)                          │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  upgradeControlPlane() 内部流程:                                                 │
│                                                                                 │
│  for _, component := range GetControlPlaneComponents() {                        │
│      // ["kube-apiserver", "kube-controller-manager", "kube-scheduler"]        │
│                                                                                 │
│      needUpgradeComponent(component):                                           │
│        ├── 读取当前运行 Pod 的 image (从 mirror Pod spec.containers[0].image)   │
│        ├── 提取当前 image tag                                                    │
│        ├── 比较: 当前 tag == BkeConfig.Cluster.KubernetesVersion?               │
│        └── 不匹配 → 需要升级                                                    │
│                                                                                 │
│      upgradeControlPlaneManifestCommand(component):                             │
│        ├── 委托给 manifestsPlugin.Execute(scope=component)                      │
│        │   └── GenerateManifestYaml(components, bootScope):                    │
│        │       ├── 读取内嵌 Go 模板:                                             │
│        │       │   tmpl/k8s/kube-apiserver.yaml.tmpl                            │
│        │       │   tmpl/k8s/kube-controller-manager.yaml.tmpl                   │
│        │       │   tmpl/k8s/kube-scheduler.yaml.tmpl                            │
│        │       ├── 渲染模板 (text/template):                                     │
│        │       │   image: {{ . | imageRepo }}{{ . | imageInfo }}                │
│        │       │        │                │                                       │
│        │       │        │                └──→ imageName:tag                     │
│        │       │        └──→ registry/repository/                               │
│        │       ├── 写入 /etc/kubernetes/manifests/<component>.yaml              │
│        │       └── systemctl restart kubelet (触发 Kubelet 重建 Pod)           │
│        │                                                                        │
│      waitComponentReady(component, beforeHash):                                 │
│        ├── 轮询 mirror Pod 的 kubernetes.io/config.hash 注解                    │
│        ├── Hash 变化 → Kubelet 已检测到 manifest 变更并重建 Pod                 │
│        └── 等待 Pod Running + Ready                                             │
│  }                                                                              │
│                                                                                 │
│  ★ kubeadm 二进制未参与 manifest 生成                                            │
│  ★ manifest 中的 image tag 来自 Go 模板渲染，来源是 BkeConfig                   │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 7.7.2 镜像 tag 的精确来源

manifest 模板中的 image 行为 `image: {{ . | imageRepo }}{{ . | imageInfo }}`，由两个模板函数拼接而成：

**`imageRepo` — 镜像仓库前缀 (含尾部 `/`)**

```go
// utils/bkeagent/mfutil/render.go — GlobalFuncMap()

"imageRepo": func(cfg *BootScope) string {
    bkeCfg := bkeinit.BkeConfig(*cfg.BkeConfig)
    return bkeCfg.ImageFuyaoRepo()  // ← 从 BKECluster.Spec.ClusterConfig.Cluster.ImageRepo 解析
},
```

```go
// common/cluster/initialize/config.go

func (bc *BkeConfig) ImageFuyaoRepo() string {
    if bc.Cluster.ImageRepo.Prefix == "" {
        return fmt.Sprintf("%s/", DefaultFuyaoImageRepo)  // "cr.openfuyao.cn/openfuyao/"
    }
    address := validation.GetImageRepoAddress(bc.Cluster.ImageRepo)
    return fmt.Sprintf("%s/%s/", address, bc.Cluster.ImageRepo.Prefix)
}
```

**`imageInfo` — 镜像名:tag (每个组件独立实现)**

```go
// kube-apiserver 的 imageInfo
"imageInfo": func(cfg *BootScope) string {
    k8sVersion := strings.TrimPrefix(cfg.BkeConfig.Cluster.KubernetesVersion, "v")
    return fmt.Sprintf("%s:%s", bkeinit.DefaultAPIServerImageName, k8sVersion)
    // → "kube-apiserver:1.35.0-of.1"
},

// kube-controller-manager 的 imageInfo
"imageInfo": func(cfg *BootScope) string {
    k8sVersion := strings.TrimPrefix(cfg.BkeConfig.Cluster.KubernetesVersion, "v")
    return fmt.Sprintf("%s:%s", bkeinit.DefaultControllerManagerImageName, k8sVersion)
    // → "kube-controller-manager:1.35.0-of.1"
},

// kube-scheduler 的 imageInfo
"imageInfo": func(cfg *BootScope) string {
    k8sVersion := strings.TrimPrefix(cfg.BkeConfig.Cluster.KubernetesVersion, "v")
    return fmt.Sprintf("%s:%s", bkeinit.DefaultSchedulerImageName, k8sVersion)
    // → "kube-scheduler:1.35.0-of.1"
},
```

**最终镜像格式**：

```
{ImageFuyaoRepo()}/{DefaultImageName}:{KubernetesVersion 去除 "v" 前缀}

示例 (KubernetesVersion = "v1.35.0-of.1"):
  cr.openfuyao.cn/openfuyao/kube-apiserver:1.35.0-of.1
  cr.openfuyao.cn/openfuyao/kube-controller-manager:1.35.0-of.1
  cr.openfuyao.cn/openfuyao/kube-scheduler:1.35.0-of.1
```

**镜像名常量**：

```go
// common/cluster/initialize/defaults.go

DefaultAPIServerImageName         = "kube-apiserver"
DefaultControllerManagerImageName = "kube-controller-manager"
DefaultSchedulerImageName         = "kube-scheduler"
DefaultEtcdImageName              = "etcd"
```

#### 7.7.3 BkeConfig 在 BKEAgent 端的获取

BKEAgent 执行升级命令时，通过 API 读取管理集群上的 BKECluster CR 获取 BkeConfig：

```go
// pkg/job/builtin/kubeadm/kubeadm.go — getBKEConfig()

func (k *KubeadmPlugin) getBKEConfig(bkeConfigNS string) error {
    bkeCluster, err := plugin.GetBKECluster(bkeConfigNS)          // 读取 BKECluster CR
    config, err := plugin.GetBkeConfigFromBkeCluster(bkeCluster)  // = bkeCluster.Spec.ClusterConfig
    k.boot.BkeConfig = config                                     // 存入 BootScope
    return nil
}

// pkg/job/builtin/plugin/interface.go

func GetBkeConfigFromBkeCluster(bkeCluster *bkev1beta1.BKECluster) (*bkev1beta1.BKEConfig, error) {
    bkeConfig := bkeCluster.Spec.ClusterConfig  // ← 直接取 Spec.ClusterConfig
    return bkeConfig, nil
}
```

**因此**：`BkeConfig.Cluster.KubernetesVersion` = `BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion` (管理集群侧已由 `SyncUpgradeTargetsToClusterSpec()` 从 VersionContext 同步)。

#### 7.7.4 kubelet/kubectl 二进制版本来源

kubelet 和 kubectl 的二进制下载也使用**同一个** `BkeConfig.Cluster.KubernetesVersion` 字段：

```go
// pkg/job/builtin/kubeadm/command.go — installKubeletCommand()

func (k *KubeadmPlugin) installKubeletCommand(immutable bool) error {
    cfg := bkeinit.BkeConfig(*k.boot.BkeConfig)
    k8sVersion := cfg.Cluster.KubernetesVersion           // ← 同一个字段
    kubeletUrl := clusterutil.BuildYumRepoDownloadBaseURL(cfg)
    kubelet := fmt.Sprintf("kubelet-%s-%s", k8sVersion, hostArch)
    // → "kubelet-v1.35.0-of.1-amd64"
    kubeletUrl = fmt.Sprintf("%s/%s", kubeletUrl, kubelet)
    // 下载 URL: http://<HTTPRepo>/kubelet-v1.35.0-of.1-amd64
}

// installKubectlCommand() 同理:
func (k *KubeadmPlugin) installKubectlCommand() error {
    cfg := bkeinit.BkeConfig(*k.boot.BkeConfig)
    k8sVersion := cfg.Cluster.KubernetesVersion           // ← 同一个字段
    kubectl := fmt.Sprintf("kubectl-%s-%s", k8sVersion, hostArch)
    // → "kubectl-v1.35.0-of.1-amd64"
}
```

**下载源**：`BkeConfig.Cluster.HTTPRepo` (HTTP 仓库地址)，通过 `BuildYumRepoDownloadBaseURL(cfg)` 构建基础 URL。

#### 7.7.5 etcd manifest 的镜像 tag 来源 (对比)

etcd 的 image tag 解析有独立的优先级链 (与 K8s 组件不同)：

```go
// utils/bkeagent/mfutil/render.go — etcdImageTagFromBootScope()

func etcdImageTagFromBootScope(cfg *BootScope) string {
    // 优先级 1: 命令参数传入的 etcdVersion (声明式升级路径)
    if v, ok := cfg.Extra["etcdVersion"]; ok {
        return strings.TrimPrefix(v, "v")
    }
    // 优先级 2: BkeConfig.Cluster.EtcdVersion
    if v := strings.TrimSpace(cfg.BkeConfig.Cluster.EtcdVersion); v != "" {
        return strings.TrimPrefix(v, "v")
    }
    // 优先级 3: 内嵌 versions.yaml 中的默认值
    return versions.EtcdImageTag()
}
```

> etcd 版本与 K8s 版本独立 (如 K8s v1.35.0-of.1 对应 etcd v3.6.7-of.1)，不共用 `KubernetesVersion` 字段。

#### 7.7.6 完整镜像 tag 来源链路

```txt
┌─────────────────────────────────────────────────────────────────────────────────┐
│              Static Pod manifest 镜像 tag 完整来源链路                            │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  管理集群:                                                                       │
│  ReleaseImage.upgrade.components[kubernetes-master].version = "v1.35.0-of.1"   │
│     │                                                                           │
│     ▼ BuildVersionContextForUpgrade                                            │
│  VersionContext.Target["kubernetes-master"] = "v1.35.0-of.1"                  │
│     │                                                                           │
│     ▼ SyncUpgradeTargetsToClusterSpec                                          │
│  BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion = "v1.35.0-of.1"    │
│     │                                                                           │
│     ▼ syncLegacyTargetKubernetesVersion (防御性)                               │
│  (确保 Spec 与 VC 一致，供 legacy 代码读取)                                     │
│     │                                                                           │
│     ▼ Command CR 创建 (携带 BKECluster namespace/name)                          │
│                                                                                 │
│  BKEAgent:                                                                      │
│  getBKEConfig() → 读取 BKECluster CR → bkeCluster.Spec.ClusterConfig           │
│     │                                                                           │
│     ▼ k.boot.BkeConfig.Cluster.KubernetesVersion = "v1.35.0-of.1"             │
│     │                                                                           │
│     ▼ GenerateManifestYaml → 渲染 Go 模板                                      │
│  image: {{ . | imageRepo }}{{ . | imageInfo }}                                 │
│     │                          │                                                │
│     │                          └── strings.TrimPrefix(KubernetesVersion, "v")  │
│     │                              → "1.35.0-of.1"                             │
│     │                              + DefaultAPIServerImageName                  │
│     │                              → "kube-apiserver:1.35.0-of.1"              │
│     │                                                                           │
│     └── ImageFuyaoRepo()                                                       │
│         = BKECluster.Spec.ClusterConfig.Cluster.ImageRepo                      │
│         → "cr.openfuyao.cn/openfuyao/"                                         │
│                                                                                 │
│     ▼ 拼接                                                                      │
│  最终 image: cr.openfuyao.cn/openfuyao/kube-apiserver:1.35.0-of.1             │
│                                                                                 │
│     ▼ 写入 /etc/kubernetes/manifests/kube-apiserver.yaml                       │
│     ▼ systemctl restart kubelet                                                │
│     ▼ Kubelet 检测 manifest 变化 → 重建 Pod (新镜像)                            │
│     ▼ waitComponentReady → 等待 Pod Running                                    │
│                                                                                 │
│  同理:                                                                          │
│  kube-controller-manager → cr.openfuyao.cn/openfuyao/kube-controller-manager:1.35.0-of.1│
│  kube-scheduler         → cr.openfuyao.cn/openfuyao/kube-scheduler:1.35.0-of.1│
│  etcd                   → cr.openfuyao.cn/openfuyao/etcd:3.6.7-of.1 (独立版本)│
│  kubelet 二进制          → kubelet-v1.35.0-of.1-amd64 (从 HTTPRepo 下载)      │
│  kubectl 二进制          → kubectl-v1.35.0-of.1-amd64 (从 HTTPRepo 下载)      │
│                                                                                 │
│  ★ 所有 K8s 组件的版本 (manifest image tag + 二进制) 都来自同一个字段:          │
│    BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion                     │
│    该字段在 DAG 执行前已由 SyncUpgradeTargetsToClusterSpec 从 VersionContext   │
│    (来源于 ReleaseImage kubernetes-master.version) 同步。                      │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

#### 7.7.7 对最小化方案的影响

| 影响项 | 说明 |
|--------|------|
| **skipKubelet 的实现可行性** | ✅ `installKubeletCommand()` 使用 `KubernetesVersion` 下载 kubelet 二进制，跳过此函数即可阻止 kubelet 升级。manifest 中的 image tag 不受影响 (apiserver/cm/scheduler manifest 独立渲染) |
| **偏差门控的 apiserver 版本来源** | ✅ apiserver 的实际 image tag = `BkeConfig.Cluster.KubernetesVersion` (去除 v 前缀)，与 `BKECluster.Status.KubernetesVersion` 一致 (升级后写入) |
| **kubelet 版本来源** | ✅ kubelet 二进制版本 = `BkeConfig.Cluster.KubernetesVersion`，跳过后保持旧版本。实际运行版本可从 `Node.NodeInfo.KubeletVersion` 读取 |
| **不需要修改 manifest 生成逻辑** | ✅ manifest 渲染逻辑不需要修改 — 只需跳过 `installKubeletCommand()` 即可。apiserver/cm/scheduler 的 manifest 仍按 `KubernetesVersion` 正常渲染 |
| **catchup-target 注解的影响** | ✅ kubelet 补充升级时，`kubelet-catchup-target` 注解覆盖 `BKECluster.Spec.KubernetesVersion`，使 `installKubeletCommand()` 下载正确中间版本的 kubelet 二进制 |

---

## 8. kubeadm upgrade apply 跳过 kubelet 的安全性分析

### 8.1 kubeadm upgrade apply 的内部步骤

```txt
kubeadm upgrade apply v1.35 执行步骤 (BKE KubeadmPlugin.upgradeControlPlane):

1. prepareUpgrade: 备份 etcd + 备份配置 + 预拉镜像
2. 替换 kube-apiserver manifest → Kubelet 自动重建 Pod
3. 替换 kube-controller-manager manifest → Kubelet 自动重建 Pod
4. 替换 kube-scheduler manifest → Kubelet 自动重建 Pod
5. installKubeletCommand: 下载 kubelet 二进制 + 替换 + systemctl restart  ← ★ 跳过此步
6. installKubectlCommand: 下载 kubectl 二进制 + 替换  ← 保留 (kubectl 偏差窗口仅 1)
```

### 8.2 跳过 step 5 的安全性

| 检查项 | 安全性 | 说明 |
|--------|--------|------|
| apiserver Pod 重建 | ✅ 安全 | Kubelet (旧版本) 通过 inotify 检测 manifest 变化，自动重建 Pod。旧版 Kubelet 能管理新版 apiserver Pod (Kubelet 不关心 Pod 内镜像版本) |
| cm/scheduler Pod 重建 | ✅ 安全 | 同上，Kubelet 仅检查 manifest Hash 变化 |
| kubelet 自身 | ✅ 安全 | kubelet 保持旧版本，K8s 允许 kubelet 滞后 apiserver 3 个小版本 (v1.25+) |
| kubectl | ✅ 安全 | kubectl 随控制面同步升级 (偏差窗口仅 1，不能延迟) |
| kube-proxy | ✅ 安全 | kube-proxy 通过 `updateAddonVersions()` 同步升级 (DaemonSet 滚动更新，无需 drain) |
| etcd | ✅ 安全 | etcd 由独立 `EnsureEtcdUpgrade` Phase 先升级，不受 kubelet 跳过影响 |

### 8.3 偏差状态时间线 (2-hop 升级)

```txt
T0 (升级前): apiserver=v1.34, kubelet=v1.34, 偏差=0 ✅

Hop 1:
T1 (apiserver manifest 替换后): apiserver=v1.35, kubelet=v1.34, 偏差=1 ✅
T2 (cm/scheduler 替换后): cm/scheduler=v1.35, kubelet=v1.34, 偏差=1 ✅
T3 (kubectl 安装后): kubectl=v1.35, kubelet=v1.34, kubectl 偏差=1 ✅
    → installKubeletCommand 被跳过 ★

Hop 2:
T4 (apiserver manifest 替换后): apiserver=v1.36, kubelet=v1.34, 偏差=2 ✅
T5 (cm/scheduler 替换后): cm/scheduler=v1.36, kubelet=v1.34, 偏差=2 ✅
T6 (kubectl 安装后): kubectl=v1.36, kubelet=v1.34, kubectl 偏差=2 ⚠️
    → kubectl 偏差=2，超过允许的 ±1！

问题: kubectl 偏差在 Hop 2 后达到 2，超过官方允许的 ±1。
解决: kubectl 在每个 hop 中同步升级 (step 6 不跳过)，但 kubectl 偏差仍可能超限。

修正: kubectl 也需要延迟升级 (与 kubelet 一起) 或在每个 hop 中升级。

分析: kubectl 的偏差规则是 ±1 (双向)，即 kubectl 可以比 apiserver 旧 1 或新 1。
      Hop 1 后: kubectl=v1.35, apiserver=v1.35 → 0 偏差 ✅
      Hop 2 后: kubectl=v1.36, apiserver=v1.36 → 0 偏差 ✅
      → kubectl 在每个 hop 中同步升级，偏差始终为 0，安全。

结论: kubectl 在每个 hop 中同步升级 (不跳过)，偏差始终为 0。
      kubelet 跳过升级，偏差从 0 增长到 2 (2-hop)，在允许的 3 范围内。
```

---

## 9. kubelet 补充升级的实现选择

### 9.1 方案 A: 复用 EnsureWorkerUpgrade (推荐)

```txt
kubelet 补充升级通过现有 EnsureWorkerUpgrade Phase 执行:

1. 设置注解: skip-kubelet=false, kubelet-catchup-target=v1.35.0
2. 设置 upgrade-ready 注解触发 BKEClusterReconciler
3. BKEClusterReconciler 执行 DAG:
   └── kubernetes-worker: EnsureWorkerUpgrade
       └── kubeadm upgrade node (仅 kubelet + kubectl)
4. EnsureWorkerUpgrade 读取 kubelet-catchup-target 注解
   └── 仅升级到指定版本 (而非 ReleaseImage 中的最终版本)
5. 逐节点: drain → kubeadm upgrade node → 健康检查 → uncordon
```

| 优势 | 说明 |
|------|------|
| 复用现有代码 | EnsureWorkerUpgrade 已有完整的逐节点 drain/upgrade/health-check 逻辑 |
| 最小修改 | 仅需 EnsureWorkerUpgrade 读取 kubelet-catchup-target 注解 |
| 成熟可靠 | worker 升级已在大规模集群验证 |

### 9.2 方案 B: 新增独立 kubelet 升级命令 (备选)

```txt
kubelet 补充升级通过新增 Command CR Phase 执行:

1. 新增 Command Phase: UpgradeKubeletOnly
2. BKEAgent 接收 Command，执行:
   ├── 下载 kubelet 二进制 (指定版本)
   ├── systemctl stop kubelet
   ├── 替换 kubelet 二进制
   ├── systemctl start kubelet
   └── 等待 Node Ready
3. 逐节点执行
```

| 优势 | 说明 |
|------|------|
| 精确控制 | 仅升级 kubelet，不触碰 kubectl |
| 独立于 worker 升级 | 不依赖 EnsureWorkerUpgrade |

| 劣势 | 说明 |
|------|------|
| 新增代码 | 需新增 Command Phase + BKEAgent 处理逻辑 |
| 重复逻辑 | drain/health-check 逻辑与 EnsureWorkerUpgrade 重复 |

> **推荐方案 A**，以最小代码修改复用现有 worker 升级机制。

---

## 10. Feature Gate

```go
// pkg/featuregate/features.go

const (
    // MinimalMultiHopUpgradeEnabled 启用最小化多 hop 升级
    // 启用后 ClusterVersionReconciler 使用 orchestrateMinimalMultiHop
    // 未启用时保持现有单 hop 升级逻辑
    MinimalMultiHopUpgradeEnabled = "MinimalMultiHopUpgradeEnabled"

    // SkipKubeletUpgradeEnabled 启用 kubelet 延迟升级
    // 启用后 EnsureMasterUpgrade 读取 skip-kubelet 注解
    // 未启用时 EnsureMasterUpgrade 正常升级 kubelet (现有行为)
    SkipKubeletUpgradeEnabled = "SkipKubeletUpgradeEnabled"
)

var defaultFeatureGates = map[string]bool{
    MinimalMultiHopUpgradeEnabled: false,
    SkipKubeletUpgradeEnabled:     false,
}
```

---

## 11. 迁移策略

| 阶段 | 版本 | 内容 | Feature Gate | 回滚方案 |
|------|------|------|-------------|---------|
| **Phase 1** | v2.7.0 | 实现 orchestrateMinimalMultiHop + skipKubelet + 偏差门控 | `MinimalMultiHopUpgradeEnabled=false` | 默认关闭，不影响现有行为 |
| **Phase 2** | v2.7.0 | 灰度启用：多 hop 升级 + kubelet 延迟 | `MinimalMultiHopUpgradeEnabled=true` | 关闭 Gate，回退到单 hop |
| **Phase 3** | v2.8.0 | 默认启用 | 默认 `true` | 可通过注解关闭 |
| **Phase 4** | v3.0.0 | 规划 KEP-14 完整重构 | 移除本方案 Gate | 迁移到独立组件方案 |

> **与 KEP-14 的关系**：本方案是 KEP-14 完整重构的**前置快速交付**。v2.7.0 使用本方案快速实现 kubelet 延迟升级，v3.0.0 规划 KEP-14 完整重构 (拆分 kubernetes-master 为独立组件)。本方案的偏差检查器 (`skew_checker.go`) 和编排逻辑 (`orchestrateMinimalMultiHop`) 在 KEP-14 重构后可直接复用。

---

## 12. 测试策略

### 12.1 单元测试

| 测试项 | 覆盖目标 | 说明 |
|--------|---------|------|
| `orchestrateMinimalMultiHop` | >85% | 多 hop 编排 + 注解设置 + 等待完成 |
| `upgradeKubeletCatchup` | >85% | kubelet 补充升级 + 中间版本计算 |
| `ComputeMinorVersionSkew` | >90% | 版本偏差计算 |
| `ComputeIntermediateVersions` | >90% | 中间版本列表计算 |
| `EnsureMasterUpgrade.skipKubelet` | >85% | skip-kubelet 注解读取 + kubeadm 参数透传 |
| `kubeadm.upgradeControlPlane.skipKubelet` | >85% | 条件跳过 installKubeletCommand |

### 12.2 集成测试

| 场景 | 验证内容 |
|------|---------|
| 2-hop 升级 (v1.34→v1.36) | kubelet 延迟 + 偏差门控 + 补充升级 |
| 3-hop 升级 (v1.34→v1.37) | 中途 kubelet 补充 (偏差达到 3) |
| 单 hop 升级 (Feature Gate 关闭) | 现有行为不变 (向后兼容) |
| kubelet 补充升级失败 | 重试 + 断点续传 |
| 偏差门控阻止 | 偏差超限时阻止继续升级 |

### 12.3 E2E 测试

| 场景 | 规模 | 验证 |
|------|------|------|
| 2-hop 升级 | 3M+5W | apiserver 先升级，kubelet 延迟，最后补充 |
| 3-hop 升级 | 3M+10W | 中途 kubelet 补充 (偏差=3 触发) |
| 大规模 | 3M+50W | kubelet 延迟减少 drain 次数验证 |

---

## 13. 工作量评估

| 任务 | 文件 | 工作量 (人日) |
|------|------|-------------|
| orchestrateMinimalMultiHop | clusterversion_controller.go | 3 |
| upgradeKubeletCatchup | clusterversion_controller.go | 1 |
| 偏差检查器 | skew_checker.go (新增) | 1 |
| EnsureMasterUpgrade 微调 | ensure_master_upgrade.go | 1.5 |
| kubeadm skipKubelet 参数 | kubeadm.go | 1 |
| 注解常量 | annotations.go | 0.5 |
| Feature Gate | features.go | 0.5 |
| 单元测试 | _test.go | 2 |
| 集成测试 | test/ | 1.5 |
| **合计** | **4 个修改 + 1 个新增** | **12 人日** |

---

## 14. 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| kubeadm 跳过 kubelet 后 Pod 重建异常 | 控制面不可用 | 低 | kubeadm 内 apiserver/cm/scheduler manifest 替换不依赖 kubelet 版本 (Kubelet 仅检查 Hash) |
| 偏差计算错误 | kubelet 无法注册 | 低 | 单元测试覆盖版本解析 + 偏差计算 |
| kubelet 补充升级超时 | 节点长时间 NotReady | 中 | 复用现有 worker 升级超时机制 + 批次控制 |
| Feature Gate 兼容性 | 新旧逻辑混用 | 中 | Feature Gate 默认关闭 + 注解控制 |
| EnsureWorkerUpgrade 复用风险 | kubectl 被意外升级 | 低 | EnsureWorkerUpgrade 读取 kubelet-catchup-target 注解，仅升级到指定版本 |
| 多 hop 注解竞态 | 注解被覆盖 | 中 | 使用 `mergecluster.SyncStatusUntilComplete` 重试机制 |

---

## 15. 附录

### 15.1 参考文档

| 文档 | 说明 |
|------|------|
| KEP-14 | K8s 核心组件迭代式升级方案设计 (完整重构方案) |
| KEP-5 | 声明式升级框架 (ClusterVersion/ReleaseImage/UpgradePath) |
| K8s Version Skew Policy | https://kubernetes.io/releases/version-skew-policy/ |
| KEP-7 CVO 架构分析 | OpenShift CVO 核心架构梳理 |
| KEP-7 BKE CVO 设计 | BKE CVO 设计方案 |

### 15.2 术语表

| 术语 | 定义 |
|------|------|
| **最小化方案** | 保留 kubernetes-master inline 不变，通过注解控制 kubelet 跳过的轻量级方案 |
| **skipKubelet** | kubeadm upgrade apply 中跳过 installKubeletCommand 步骤的参数 |
| **偏差门控** | 在 hop 之间检查 kubelet vs apiserver 偏差，决定是否触发补充升级 |
| **kubelet 补充升级** | kubelet 延迟到偏差达到极限后，逐版本升级到目标版本的过程 |
| **hopPath** | openFuyao 版本路径，编排器逐 hop 推进 |
| **K8sSkewConstraints** | K8s 官方版本偏差约束 (kubelet ≤ apiserver 3 个小版本) |

---

**文档版本**: v1.0
**维护者**: openFuyao Team

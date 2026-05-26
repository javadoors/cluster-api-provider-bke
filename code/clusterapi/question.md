# 问题
## 问题 1：绕过 KCP 声明式升级，自研命令式 Master 升级

### BKE 当前做法（命令式）

[ensure_master_upgrade.go:60-65](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go#L60-L65) — 通过 annotation 标记升级意图，然后手动逐节点编排：

```go
// ensure_master_upgrade.go - BKE 自研命令式升级
func (e *EnsureMasterUpgrade) Execute() (ctrl.Result, error) {
    // ❌ 问题1：通过 annotation 侧信道标记升级，而非 Spec 声明
    if v, ok := annotation.HasAnnotation(e.Ctx.BKECluster, "deployAction"); !ok || v != "k8s_upgrade" {
        patchFunc := func(bkeCluster *bkev1beta1.BKECluster) {
            annotation.SetAnnotation(bkeCluster, "deployAction", "k8s_upgrade")  // 侧信道
        }
        if err := mergecluster.SyncStatusUntilComplete(e.Ctx.Client, e.Ctx.BKECluster, patchFunc); err != nil {
            return ctrl.Result{}, err
        }
    }
    return e.reconcileMasterUpgrade()
}

// ❌ 问题2：手动逐节点 for 循环升级，一个失败全部阻塞
func (e *EnsureMasterUpgrade) upgradeMasterNodesWithParams(params UpgradeMasterNodesParams) error {
    for _, node := range params.NeedUpgradeNodes {
        // 手动标记节点状态
        nodeFetcher.SetNodeStateWithMessageForCluster(params.Ctx, params.BKECluster, node.IP, bkev1beta1.NodeUpgrading, "Upgrading")
        
        if err := e.upgradeNode(params.NeedBackupEtcd, params.BackEtcdNode, node, remoteNode); err != nil {
            // ❌ 问题3：单个节点失败直接 return error，后续节点不再尝试
            nodeFetcher.SetNodeStateWithMessageForCluster(params.Ctx, params.BKECluster, node.IP, bkev1beta1.NodeUpgradeFailed, err.Error())
            return errors.Errorf("upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
        }
    }
    // ❌ 问题4：手动更新 Status 版本
    bkeCluster.Status.KubernetesVersion = bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion
    return nil
}
```

### CAPI 标准做法（声明式）
```go
// ✅ 标准 CAPI 声明式升级：只需修改 KCP Spec.Version，KCP 控制器自动编排滚动升级
func (r *BKEClusterReconciler) reconcileUpgrade(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) error {
    kcp := &controlplanev1.KubeadmControlPlane{}
    if err := r.Get(ctx, client.ObjectKey{Namespace: bkeCluster.Namespace, Name: bkeCluster.Name + "-kcp"}, kcp); err != nil {
        return err
    }

    // 只需修改 KCP 的 Version 字段，KCP 控制器自动处理：
    // 1. 逐个创建新 Machine
    // 2. 等待新 Machine 就绪
    // 3. 驱逐旧节点 Pod
    // 4. 删除旧 Machine
    // 5. 更新 Status
    if kcp.Spec.Version != bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion {
        kcp.Spec.Version = bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion
        if err := r.Update(ctx, kcp); err != nil {
            return err
        }
    }
    return nil
}
```
**差距**：BKE 写了 **400+ 行** 代码实现 Master 升级，而 CAPI 标准方式只需 **修改一个字段**。

## 问题 2：暂停 KCP/MD 后自研升级，绕过 CAPI 声明式能力

### BKE 当前做法
[ensure_paused.go:142-176](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_paused.go#L142-L176) — 升级时暂停 KCP/MD，然后自研 Phase 手动编排：

```go
// ensure_paused.go - 升级时暂停 CAPI 对象
func (e *EnsurePaused) pauseOrResumeClusterAPIObjs(params PauseOperationParams) error {
    kcp, _ := phaseutil.GetClusterAPIKubeadmControlPlane(params.Ctx, params.Client, e.Ctx.Cluster)
    md, _ := phaseutil.GetClusterAPIMachineDeployment(params.Ctx, params.Client, e.Ctx.Cluster)

    if params.BKECluster.Spec.Pause {
        // ❌ 暂停 KCP，使其不再处理 Machine 生命周期
        if kcp != nil {
            if err := phaseutil.PauseClusterAPIObj(params.Ctx, params.Client, kcp); err != nil {
                return err
            }
        }
        if md != nil {
            if err := phaseutil.PauseClusterAPIObj(params.Ctx, params.Client, md); err != nil {
                return err
            }
        }
    } else {
        // ❌ 升级/扩缩容阶段不恢复 CAPI 对象，由自研 Phase 接管
        if params.BKECluster.Status.Phase == bkev1beta1.Scale || 
           params.BKECluster.Status.Phase == bkev1beta1.UpgradeControlPlane || 
           params.BKECluster.Status.Phase == bkev1beta1.UpgradeWorker {
            return nil  // 不恢复，继续由自研 Phase 控制
        }
    }
    return nil
}

// clusterapi.go - 暂停实现就是加 annotation
func PauseClusterAPIObj(ctx context.Context, c client.Client, obj client.Object, ...) error {
    annotations := obj.GetAnnotations()
    annotations[clusterv1beta1.PausedAnnotation] = ""  // ❌ 加 paused 注解，KCP/MD 控制器停止工作
    obj.SetAnnotations(annotations)
    return c.Update(ctx, obj)
}
```

### CAPI 标准做法
```go
// ✅ 标准 CAPI：永远不需要暂停 KCP/MD
// 升级 = 修改 KCP.Spec.Version → KCP 控制器自动滚动升级
// 扩容 = 修改 KCP.Spec.Replicas → KCP 控制器自动创建 Machine
// 缩容 = 修改 KCP.Spec.Replicas → KCP 控制器自动删除 Machine

func (r *BKEClusterReconciler) reconcileScale(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) error {
    kcp := &controlplanev1.KubeadmControlPlane{}
    if err := r.Get(ctx, client.ObjectKeyFromObject(bkeCluster), kcp); err != nil {
        return err
    }

    desiredReplicas := int32(len(bkeCluster.Spec.ClusterConfig.MasterNodes))
    if *kcp.Spec.Replicas != desiredReplicas {
        kcp.Spec.Replicas = &desiredReplicas
        return r.Update(ctx, kcp)  // KCP 控制器自动处理 Machine 创建/删除
    }
    return nil
}
```
**差距**：BKE 引入了 CAPI 但**升级/扩缩容时主动暂停 CAPI 控制器**，然后自研 Phase 接管，等于 CAPI 的声明式能力完全被架空。

## 问题 3：升级 Phase 顺序硬编码，无法并行

### BKE 当前做法
[list.go:40-57](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/list.go#L40-L57) — 升级 Phase 数组固定线性执行：

```go
// list.go - 升级 Phase 硬编码线性顺序
PostDeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
    NewEnsureProviderSelfUpgrade,   // 1. Provider 自升级
    NewEnsureAgentUpgrade,          // 2. Agent 升级
    NewEnsureContainerdUpgrade,     // 3. Containerd 升级
    NewEnsureEtcdUpgrade,           // 4. Etcd 升级
    NewEnsureWorkerUpgrade,         // 5. Worker 升级
    NewEnsureMasterUpgrade,         // 6. Master 升级
    NewEnsureWorkerDelete,          // 7. Worker 删除
    NewEnsureMasterDelete,          // 8. Master 删除
    NewEnsureComponentUpgrade,      // 9. 组件升级
    NewEnsureCluster,               // 10. 集群状态
}

// phase_flow.go - 串行执行所有 Phase
func (p *PhaseFlow) executePhases(phases confv1beta1.BKEClusterPhases) (ctrl.Result, error) {
    for _, phase := range p.BKEPhases {
        if phase.Name().In(phases) {
            if phase.NeedExecute(p.oldBKECluster, p.newBKECluster) {
                phaseResult, phaseErr := phase.Execute()  // ❌ 串行执行，无法并行
                if phaseErr != nil {
                    errs = append(errs, phaseErr)
                }
            }
        }
    }
    return res, nil
}
```

### CAPI v1.12 标准做法（DAG + Chained Upgrades）
```go
// ✅ CAPI v1.12 Chained Upgrades：声明目标版本，自动编排中间步骤
// 用户只需声明最终状态，CAPI 自动计算升级路径

apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: my-cluster
spec:
  topology:
    class: bke-cluster-class
    version: v1.30.0   # 从 v1.26 直接声明到 v1.30，CAPI 自动链式升级
                        # v1.26 → v1.27 → v1.28 → v1.29 → v1.30

---
# ✅ CAPI v1.12 In-place Updates：无需删除重建 Machine
apiVersion: cluster.x-k8s.io/v1beta1
kind: KubeadmControlPlane
spec:
  version: v1.30.0
  updateStrategy:
    type: InPlace       # 原地更新，不需要创建新 Machine
    inPlaceUpdate:
      maxUnavailable: 1
```

**差距**：BKE 的 10 个升级 Phase 串行执行，Agent 升级和 Containerd 升级无依赖关系也必须串行等待。CAPI v1.12 的 DAG 编排可以自动识别依赖关系并行执行。

## 问题 4：kubectl 版本硬编码

### BKE 当前做法
[ensure_master_upgrade.go:254](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go#L254) — kubectl 目标版本硬编码为 `v1.25`：

```go
// ❌ kubectl 版本硬编码
func (e *EnsureMasterUpgrade) updateAddonVersions(c client.Client, bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger) (ctrl.Result, error) {
    for _, addon := range bkeCluster.Spec.ClusterConfig.Addons {
        if addon.Name == "kubectl" && addon.Version != "v1.25" {  // ❌ 硬编码 v1.25
            log.Info(constant.MasterUpgradingReason, "kubectl need upgrade")
            kubectlNeedUpgrade = true
        }
    }

    if kubectlNeedUpgrade {
        patchFunc := func(currentCombinedBkeCluster *bkev1beta1.BKECluster) {
            for i, d := range currentCombinedBkeCluster.Spec.ClusterConfig.Addons {
                if d.Name == "kubectl" {
                    d.Version = "v1.25"  // ❌ 强制设为 v1.25，与 K8s 版本无关
                    currentCombinedBkeCluster.Spec.ClusterConfig.Addons[i] = d
                }
            }
        }
    }
}
```

### CAPI + ClusterClass 标准做法
```yaml
# ✅ ClusterClass 中组件版本跟随 K8s 版本声明，无硬编码
apiVersion: cluster.x-k8s.io/v1beta1
kind: ClusterClass
metadata:
  name: bke-cluster-class
spec:
  controlPlane:
    ref:
      apiVersion: controlplane.cluster.x-k8s.io/v1beta1
      kind: KubeadmControlPlaneTemplate
      name: bke-control-plane
  variables:
    - name: kubectlVersion
      required: false
      schema:
        openAPIV3Schema:
          type: string
          default: ""   # 留空则自动跟随 K8s 版本
---
# 用户创建集群时声明版本
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
spec:
  topology:
    class: bke-cluster-class
    version: v1.30.0   # kubectl 版本自动对齐
```

---

## 问题 5：Worker 升级失败处理矛盾

### BKE 当前做法

[ensure_worker_upgrade.go:244-278](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_upgrade.go#L244-L278) — 先 continue 跳过失败节点，最后又报错：

```go
// ❌ 矛盾的失败处理策略
func (e *EnsureWorkerUpgrade) processNodeUpgrade(params ProcessNodeUpgradeParams) (ctrl.Result, []string, error) {
    var failedUpgradeNodes []string
    for _, node := range params.NeedUpgradeNodes {
        if err := e.upgradeNode(node, remoteNode, params.Drainer); err != nil {
            failedUpgradeNodes = append(failedUpgradeNodes, phaseutil.NodeInfo(node))
            // ❌ 先 continue 跳过失败节点，继续升级下一个
            continue
        }
    }
    // ❌ 但最后如果有失败节点，整体返回 error
    // 下次 Reconcile 会重新尝试所有节点，而非仅失败节点
    return ctrl.Result{}, failedUpgradeNodes, nil
}

func (e *EnsureWorkerUpgrade) rolloutUpgrade() (ctrl.Result, error) {
    _, failedUpgradeNodes, err := e.processNodeUpgrade(upgradeParams)
    if len(failedUpgradeNodes) == 0 {
        return ctrl.Result{}, nil
    } else {
        // ❌ 整体报错，下次重试所有节点而非仅失败节点
        return ctrl.Result{}, errors.Errorf("upgrade worker process finished, but some nodes upgrade failed, nodes: %v", failedUpgradeNodes)
    }
}
```

### CAPI 标准做法（MachineDeployment RollingUpdate）
```go
// ✅ CAPI MachineDeployment 滚动更新：自动处理失败节点
// 只需修改 MD.Spec.Template.Spec.Version，MD 控制器自动：
// 1. 按 maxUnavailable/maxSurge 策略并行升级
// 2. 单个 Machine 失败不影响其他 Machine
// 3. 自动重试失败 Machine
// 4. 已成功的 Machine 不会重复升级

func (r *BKEClusterReconciler) reconcileWorkerUpgrade(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) error {
    mdList := &clusterv1.MachineDeploymentList{}
    if err := r.List(ctx, mdList, client.MatchingLabels{"cluster.x-k8s.io/cluster-name": bkeCluster.Name}); err != nil {
        return err
    }
    for _, md := range mdList.Items {
        if md.Spec.Template.Spec.Version != nil && *md.Spec.Template.Spec.Version != bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion {
            md.Spec.Template.Spec.Version = ptr.To(bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion)
            if err := r.Update(ctx, &md); err != nil {
                return err
            }
        }
    }
    return nil
}
```

## 问题 6：升级完全无回滚能力

### BKE 当前做法
所有升级 Phase 的 Execute() 方法均无回滚逻辑：
```go
// ensure_master_upgrade.go - 升级失败后只标记状态，无回滚
if err := e.upgradeNode(params.NeedBackupEtcd, params.BackEtcdNode, node, remoteNode); err != nil {
    nodeFetcher.SetNodeStateWithMessageForCluster(params.Ctx, params.BKECluster, node.IP, bkev1beta1.NodeUpgradeFailed, err.Error())
    // ❌ 仅标记失败，无回滚到升级前版本的逻辑
    return errors.Errorf("upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
}
```

### CAPI v1.12 标准做法（In-place Updates + 回滚）
```yaml
# ✅ CAPI v1.12：声明式回滚 = 修改 Spec.Version 回旧版本
# KCP 控制器自动执行滚动回滚
apiVersion: controlplane.cluster.x-k8s.io/v1beta1
kind: KubeadmControlPlane
spec:
  version: v1.28.0    # 从 v1.30.0 回退到 v1.28.0，KCP 自动回滚
  rolloutStrategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0    # 保证零停机回滚
```

```go
// ✅ 回滚只需修改版本号
func (r *BKEClusterReconciler) rollbackUpgrade(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) error {
    kcp := &controlplanev1.KubeadmControlPlane{}
    if err := r.Get(ctx, client.ObjectKeyFromObject(bkeCluster), kcp); err != nil {
        return err
    }
    // 回滚 = 把版本改回去
    kcp.Spec.Version = bkeCluster.Status.KubernetesVersion  // 回到当前运行版本
    return r.Update(ctx, kcp)
}
```

## 总结对比表
| 问题 | BKE 代码量 | CAPI 标准代码量 | 差距本质 |
|------|-----------|----------------|---------|
| Master 升级 | 400+ 行（[ensure_master_upgrade.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_upgrade.go)） | **1 行**（`kcp.Spec.Version = newVersion`） | 命令式 vs 声明式 |
| Worker 升级 | 300+ 行（[ensure_worker_upgrade.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_upgrade.go)） | **1 行**（`md.Spec.Template.Spec.Version = newVersion`） | 命令式 vs 声明式 |
| 暂停/恢复 KCP/MD | [clusterapi.go:129-174](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phaseutil/clusterapi.go#L129-L174) + [ensure_paused.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/ensure_paused.go) | **不需要** | 架架空 CAPI |
| 升级 Phase 编排 | [list.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/list.go) + [phase_flow.go](file:///d:/code/github/aaa/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go) 200+ 行 | **不需要**（KCP/MD 控制器内置） | 重复造轮子 |
| 回滚 | **不存在** | **1 行**（改回旧版本号） | 缺失关键能力 |

**核心结论**：BKE 的升级架构问题本质是**引入了 CAPI 但绕过了其声明式核心能力**，自研了一套命令式 Phase 编排来替代 KCP/MD 控制器。升级 CAPI 到 v1.12 的同时，必须同步删除自研 Phase，让 KCP/MD 控制器接管升级/扩缩容逻辑。
        

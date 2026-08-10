# K8s 跨版本升级版本倾斜合规方案

> 基于 cluster-api-provider-bke 代码库分析

## 一、版本倾斜约束分析

### 1.1 Kubernetes 版本倾斜规则

| 组件对 | 约束 | 说明 |
|--------|------|------|
| **API Server ↔ kubelet** | kubelet <= API Server，且 API Server - kubelet <= 2 | 核心约束 |
| **API Server ↔ Controller Manager** | 必须同版本 | 同节点静态 Pod |
| **API Server ↔ Scheduler** | 必须同版本 | 同节点静态 Pod |
| **kubelet ↔ kube-proxy** | kube-proxy <= kubelet | Worker 节点 |

### 1.2 BKE 中的升级时序（代码已实现）

**Master 节点**（`kubeadm.go:279-342`）：

```
API Server → Controller Manager → Scheduler → kubelet → kubectl
```

**关键事实**：Master 节点上 API Server 和 kubelet 在同一次 `UpgradeControlPlane` 命令中一起升级，但 API Server 先完成。

### 1.3 跨版本升级时的版本倾斜违规

场景：K8s 1.25 → 1.29（跨 4 个小版本）

```
时间线（当前实现，无分阶段）：

t0: API Server = 1.25, Master kubelet = 1.25, Worker kubelet = 1.25
    倾斜: 0 ✓

t1: Master API Server = 1.29, Master kubelet = 1.25, Worker kubelet = 1.25
    倾斜: 4 ✗ (违反 ≤2 约束！)

t2: Master API Server = 1.29, Master kubelet = 1.29, Worker kubelet = 1.25
    倾斜: 4 ✗ (Master 节点内部解决了，但 Worker 仍违规)

t3: Master API Server = 1.29, Master kubelet = 1.29, Worker kubelet = 1.29
    倾斜: 0 ✓
```

**问题**：t1→t3 期间，API Server 1.29 与 Worker kubelet 1.25 的版本倾斜为 4，严重违反 ≤2 约束。

## 二、核心设计思路：分阶段升级

### 2.1 基本思路

将一次大跨度升级拆分为多个阶段，每个阶段 K8s 版本跨度 ≤ 2（满足版本倾斜约束），每个阶段完成后所有组件版本倾斜合规。

```
K8s 1.25 → 1.29（跨 4 个小版本，maxSkew = 2）

拆分为 2 个阶段：
  阶段 1: 1.25 → 1.27（跨度 2，≤ maxSkew ✓）
  阶段 2: 1.27 → 1.29（跨度 2，≤ maxSkew ✓）
```

### 2.2 分阶段升级时序

```
阶段 1: K8s 1.25 → 1.27
  │
  ├── Step 1: 所有 Master 节点升级到 1.27
  │   (API Server 1.27 + Master kubelet 1.27)
  │   Worker kubelet 仍为 1.25
  │   倾斜: API Server(1.27) - Worker kubelet(1.25) = 2 ✓
  │
  ├── Step 2: 所有 Worker 节点升级到 1.27
  │   全部 1.27
  │   倾斜: 0 ✓
  │
  └── 阶段 1 完成 ✓

阶段 2: K8s 1.27 → 1.29
  │
  ├── Step 3: 所有 Master 节点升级到 1.29
  │   (API Server 1.29 + Master kubelet 1.29)
  │   Worker kubelet 仍为 1.27
  │   倾斜: API Server(1.29) - Worker kubelet(1.27) = 2 ✓
  │
  ├── Step 4: 所有 Worker 节点升级到 1.29
  │   全部 1.29
  │   倾斜: 0 ✓
  │
  └── 阶段 2 完成 ✓
```

**每个时刻的版本倾斜验证**：

| 时刻 | API Server | Master kubelet | Worker kubelet | 最大倾斜 | 合规 |
|------|-----------|---------------|---------------|---------|------|
| t0 | 1.25 | 1.25 | 1.25 | 0 | ✓ |
| t1 (阶段1 Master升级后) | 1.27 | 1.27 | 1.25 | 2 | ✓ |
| t2 (阶段1 Worker升级后) | 1.27 | 1.27 | 1.27 | 0 | ✓ |
| t3 (阶段2 Master升级后) | 1.29 | 1.29 | 1.27 | 2 | ✓ |
| t4 (阶段2 Worker升级后) | 1.29 | 1.29 | 1.29 | 0 | ✓ |

## 三、中间版本自动计算算法

### 3.1 算法设计

```go
// pkg/upgrade/version_skew.go

const MaxK8sVersionSkew = 2  // Kubernetes 版本倾斜上限

// ComputeIntermediateVersions 计算从 current 到 target 的中间版本列表
// 返回: [intermediate_1, intermediate_2, ..., target]
// 保证相邻版本跨度 <= MaxK8sVersionSkew
func ComputeIntermediateVersions(current, target string) ([]string, error) {
    currentMajor, currentMinor, err := parseMajorMinor(current)
    if err != nil {
        return nil, err
    }
    targetMajor, targetMinor, err := parseMajorMinor(target)
    if err != nil {
        return nil, err
    }

    if currentMajor != targetMajor {
        return nil, fmt.Errorf("cross-major-version upgrade not supported: %s → %s", current, target)
    }

    if targetMinor <= currentMinor {
        return nil, fmt.Errorf("downgrade not supported: %s → %s", current, target)
    }

    span := targetMinor - currentMinor
    if span <= MaxK8sVersionSkew {
        // 跨度 <= 2，无需中间版本
        return []string{target}, nil
    }

    // 计算中间版本：每次跳 MaxK8sVersionSkew 步
    var intermediates []string
    current := currentMinor
    for current+MaxK8sVersionSkew < targetMinor {
        current += MaxK8sVersionSkew
        intermediates = append(intermediates,
            fmt.Sprintf("%d.%d", currentMajor, current))
    }

    // 最后一步到目标版本
    if current != targetMinor {
        intermediates = append(intermediates, target)
    }

    return intermediates, nil
}
```

### 3.2 算法示例

| 当前版本 | 目标版本 | 跨度 | 中间版本 | 阶段数 |
|---------|---------|------|---------|--------|
| 1.25 | 1.26 | 1 | [1.26] | 1 |
| 1.25 | 1.27 | 2 | [1.27] | 1 |
| 1.25 | 1.28 | 3 | [1.27, 1.28] | 2 |
| 1.25 | 1.29 | 4 | [1.27, 1.29] | 2 |
| 1.25 | 1.30 | 5 | [1.27, 1.29, 1.30] | 3 |
| 1.25 | 1.31 | 6 | [1.27, 1.29, 1.31] | 3 |

### 3.3 中间版本的 ReleaseImage 获取

每个中间版本需要对应的 ReleaseImage Bundle（包含该版本的 etcd、containerd、K8s 等组件版本）：

```go
// 中间版本的 ReleaseImage 命名约定
// openFuyao v26.06 对应 K8s 1.29
// 中间版本 K8s 1.27 需要找到对应的 openFuyao 版本

func resolveIntermediateReleaseImage(ctx context.Context, k8sVersion string) (*manifest.Bundle, error) {
    // 方案 1: 通过 UpgradePath CRD 查找
    //   UpgradePath 中可以定义 openFuyao 版本与 K8s 版本的映射

    // 方案 2: 通过 ReleaseImage CR 查找
    //   遍历所有 ReleaseImage CR，找到包含目标 K8s 版本的 ReleaseImage

    // 方案 3: 通过版本配置文件查找
    //   维护一个 openFuyao 版本 → K8s 版本的映射表
}
```

## 四、DAG 编排改造

### 4.1 改造方案：多 DAG 分阶段执行

当前 `executeUpgradeDAG()` 执行单个 DAG。改造为分阶段执行多个 DAG：

```go
// controllers/capbke/bkecluster_upgrade_dag.go 改造

func (r *BKEClusterReconciler) executeUpgradeDAG(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    targetVersion string,
) (ctrl.Result, error) {
    // 1. 获取当前和目标 K8s 版本
    currentK8sVersion := bkeCluster.Status.KubernetesVersion
    targetK8sVersion := r.getTargetK8sVersion(bkeCluster, targetVersion)

    // 2. 计算中间版本
    stages, err := upgrade.ComputeIntermediateVersions(currentK8sVersion, targetK8sVersion)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 3. 逐阶段执行
    for i, stageVersion := range stages {
        // 3.1 获取该阶段的 ReleaseImage Bundle
        stageBundle, err := r.resolveK8sVersionBundle(ctx, stageVersion)
        if err != nil {
            return ctrl.Result{}, err
        }

        // 3.2 构建该阶段的 VersionContext
        //   Current = 上一阶段完成后的版本
        //   Target = 该阶段目标版本
        stageVC := upgrade.BuildVersionContextForUpgrade(stageBundle, currentBundle, bkeCluster)

        // 3.3 构建并执行该阶段的 DAG
        stageDAG, err := upgrade.BuildDAGFromBundle(stageBundle, upgrade.BundleDependencyResolver(stageBundle))
        if err != nil {
            return ctrl.Result{}, err
        }

        // 3.4 执行 DAG
        sched := dagexec.NewScheduler(dagexec.Config{...})
        if err := sched.ExecuteDAG(ctx, phaseCtx, oldCluster, newCluster, stageDAG); err != nil {
            return ctrl.Result{}, err
        }

        // 3.5 阶段完成后验证版本倾斜
        if err := r.verifyVersionSkewCompliance(ctx, bkeCluster); err != nil {
            return ctrl.Result{}, err
        }

        // 3.6 更新 currentBundle 为当前阶段完成后的状态
        currentBundle = stageBundle
    }

    // 4. 完成升级
    return r.completeDeclarativeUpgrade(ctx, bkeCluster, targetVersion)
}
```

### 4.2 阶段状态持久化

在 `DeclarativeUpgradeStatus` 中增加阶段信息：

```go
// api/bkecommon/v1beta1/bkecluster_status.go 扩展

type DeclarativeUpgradeStatus struct {
    TargetVersion string
    StartedAt     *metav1.Time
    FinishedAt    *metav1.Time
    LastError     string
    LastFailure   *DeclarativeUpgradeFailureRecord
    Completed     []DeclarativeUpgradeComponentRecord

    // 新增：分阶段升级信息
    CurrentStage     int      // 当前阶段索引（从 0 开始）
    TotalStages      int      // 总阶段数
    StageVersions    []string // 各阶段目标 K8s 版本
    StageCompleted   []bool   // 各阶段是否完成
}
```

### 4.3 断点续传支持

如果升级在阶段 2 失败，重试时从阶段 2 继续：

```go
func (r *BKEClusterReconciler) executeUpgradeDAG(...) {
    // 检查是否有未完成的分阶段升级
    if status.CurrentStage > 0 && !isAllStagesCompleted(status) {
        // 从 CurrentStage 继续
        for i := status.CurrentStage; i < len(stages); i++ {
            if status.StageCompleted[i] {
                continue // 跳过已完成的阶段
            }
            // 执行该阶段...
        }
    }
}
```

## 五、运行时版本倾斜检查

### 5.1 升级前检查

在 `EnsureMasterUpgrade` 和 `EnsureWorkerUpgrade` 中增加版本倾斜检查：

```go
// pkg/phaseframe/phases/version_skew_check.go

func checkVersionSkewCompliance(ctx context.Context, client client.Client, bc *bkev1beta1.BKECluster) error {
    remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, client, bc)
    if err != nil {
        return err
    }
    clientSet, _ := remoteClient.KubeClient()

    // 1. 获取 API Server 版本
    apiServerVersion, err := getAPIServerVersion(clientSet)
    if err != nil {
        return err
    }

    // 2. 获取所有节点的 kubelet 版本
    nodes, err := clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
    if err != nil {
        return err
    }

    // 3. 检查每个节点的版本倾斜
    apiMinor := parseMinor(apiServerVersion)
    for _, node := range nodes.Items {
        kubeletMinor := parseMinor(node.Status.NodeInfo.KubeletVersion)
        skew := apiMinor - kubeletMinor

        if skew > MaxK8sVersionSkew {
            return fmt.Errorf(
                "version skew violation: API Server %s, node %s kubelet %s, skew = %d (max = %d)",
                apiServerVersion, node.Name, node.Status.NodeInfo.KubeletVersion, skew, MaxK8sVersionSkew)
        }
        if skew < 0 {
            return fmt.Errorf(
                "version skew violation: node %s kubelet %s is newer than API Server %s",
                node.Name, node.Status.NodeInfo.KubeletVersion, apiServerVersion)
        }
    }

    return nil
}
```

### 5.2 升级前拦截

在 `EnsureMasterUpgrade.NeedExecute()` 中调用：

```go
func (e *EnsureMasterUpgrade) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    // ... 现有检查 ...

    // 新增：版本倾斜预检
    targetVersion := e.desiredKubernetesVersion()
    currentVersion := e.currentKubernetesVersion()
    if isMultiMinorVersionUpgrade(currentVersion, targetVersion) {
        // 检查是否有分阶段升级计划
        if !e.hasStagedUpgradePlan() {
            e.Ctx.Log.Error("version skew",
                "cross-version upgrade from %s to %s requires staged upgrade",
                currentVersion, targetVersion)
            return false // 阻塞升级
        }
    }

    return true
}
```

## 六、升级流程图

```
openFuyao 26.03 → 26.06 (K8s 1.25 → 1.29)
  │
  v
ClusterVersionReconciler
  ├── 解析目标 ReleaseImage → K8s 1.29
  ├── 读取当前 K8s 版本 → 1.25
  │
  v
ComputeIntermediateVersions(1.25, 1.29)
  └── 返回: [1.27, 1.29]（2 个阶段）
  │
  v
┌─────────────────────────────────────────────┐
│ 阶段 1: K8s 1.25 → 1.27                     │
│                                               │
│ 1. 解析 K8s 1.27 对应的 ReleaseImage Bundle   │
│ 2. 构建 VersionContext                        │
│    Current: kubernetes-master = 1.25          │
│    Target:  kubernetes-master = 1.27          │
│ 3. 构建 DAG 并执行                            │
│    ├── pre-upgrade-resources                  │
│    ├── bkeagent                               │
│    ├── containerd                             │
│    ├── etcd                                   │
│    ├── kubernetes-master (API Server 1.27)    │
│    │   └── Master 节点: API Server + kubelet  │
│    │       版本倾斜: 1.27 - 1.25(Worker) = 2 ✓│
│    ├── kubernetes-worker (kubelet 1.27)       │
│    │   └── Worker 节点: kubelet 1.27          │
│    │       版本倾斜: 0 ✓                      │
│    ├── kube-proxy                             │
│    └── coredns                                │
│ 4. 验证版本倾斜合规                           │
│ 5. 标记阶段 1 完成                            │
└─────────────────────────────────────────────┘
  │
  v
┌─────────────────────────────────────────────┐
│ 阶段 2: K8s 1.27 → 1.29                     │
│                                               │
│ 1. 解析 K8s 1.29 对应的 ReleaseImage Bundle   │
│ 2. 构建 VersionContext                        │
│    Current: kubernetes-master = 1.27          │
│    Target:  kubernetes-master = 1.29          │
│ 3. 构建 DAG 并执行                            │
│    ├── kubernetes-master (API Server 1.29)    │
│    │   └── Master 节点: API Server + kubelet  │
│    │       版本倾斜: 1.29 - 1.27(Worker) = 2 ✓│
│    ├── kubernetes-worker (kubelet 1.29)       │
│    │   └── Worker 节点: kubelet 1.29          │
│    │       版本倾斜: 0 ✓                      │
│    └── ...                                    │
│ 4. 验证版本倾斜合规                           │
│ 5. 标记阶段 2 完成                            │
└─────────────────────────────────────────────┘
  │
  v
completeDeclarativeUpgrade()
  └── 升级完成，K8s 1.29 ✓
```

## 七、升级失败回滚方案

### 7.1 回滚场景分析

#### 7.1.1 分阶段升级的失败点

以 K8s 1.25 → 1.29（拆分为 1.27、1.29 两个阶段）为例，分析各阶段可能的失败点：

```
阶段 1: K8s 1.25 → 1.27
  │
  ├── 失败点 A: etcd 升级失败
  │   状态: etcd 部分节点升级失败，K8s 组件未开始升级
  │   影响: etcd 集群可能不可用
  │
  ├── 失败点 B: Master 节点升级失败
  │   状态: API Server = 1.27(部分), Master kubelet = 1.27(部分), Worker kubelet = 1.25
  │   影响: 控制面部分不可用，版本倾斜 = 2（合规）
  │
  └── 失败点 C: Worker 节点升级失败
      状态: Master = 1.27, 部分 Worker = 1.27, 部分 Worker = 1.25
      影响: 部分工作负载不可用，版本倾斜 = 2（合规）

阶段 2: K8s 1.27 → 1.29
  │
  ├── 失败点 D: Master 节点升级失败
  │   状态: API Server = 1.29(部分), Master kubelet = 1.29(部分), Worker kubelet = 1.27
  │   影响: 控制面部分不可用，版本倾斜 = 2（合规）
  │
  └── 失败点 E: Worker 节点升级失败
      状态: Master = 1.29, 部分 Worker = 1.29, 部分 Worker = 1.27
      影响: 部分工作负载不可用，版本倾斜 = 2（合规）
```

#### 7.1.2 回滚决策树

```
升级失败
  │
  ├── 失败点 A: etcd 升级失败
  │   └── 策略: 阶段内重试 → 仍失败 → 回滚 etcd 到当前阶段起始版本
  │
  ├── 失败点 B/C: 阶段 1 中 Master/Worker 升级失败
  │   ├── 首选: 阶段内重试（修复问题后重试失败节点）
  │   ├── 次选: 回滚当前阶段已升级节点到阶段起始版本（1.25）
  │   └── 最后: 完全回滚（如果阶段内回滚也失败）
  │
  └── 失败点 D/E: 阶段 2 中 Master/Worker 升级失败
      ├── 首选: 阶段内重试（修复问题后重试失败节点）
      ├── 次选: 回滚当前阶段已升级节点到阶段起始版本（1.27）
      └── 最后: 回退到阶段 1 完成状态（1.27），放弃阶段 2
```

### 7.2 回滚策略设计

#### 7.2.1 策略一：阶段内重试（推荐）

**设计思路**：
- 当前阶段失败后，先尝试重试当前阶段
- 利用现有的 `DeclarativeUpgradeStatus.Completed` 跳过已完成组件
- 从失败点继续执行，不需要回滚已成功的组件

**适用场景**：
- 瞬时故障（网络超时、镜像拉取失败）
- 部分节点升级失败（其他节点已成功）
- 健康检查超时（组件启动慢）

**执行流程**：
```
阶段 N 失败
  │
  v
1. 记录失败信息到 DeclarativeUpgradeStatus.LastFailure
  │
  v
2. 等待用户修复问题（或自动重试）
  │
  v
3. 重新触发 Reconcile
  │
  v
4. 检查 DeclarativeUpgradeStatus
  ├── CurrentStage = N（当前阶段）
  ├── StageCompleted[N] = false（当前阶段未完成）
  └── Completed 列表包含已成功的组件
  │
  v
5. 从失败点继续执行阶段 N
  ├── 跳过 Completed 中的组件
  └── 重新执行失败的组件
```

**优点**：
- ✅ 实现简单，复用现有断点续传机制
- ✅ 不回滚已成功的组件，减少影响范围
- ✅ 与现有升级流程一致

**缺点**：
- ❌ 如果根本问题未解决，可能反复失败

#### 7.2.2 策略二：阶段内回滚

**设计思路**：
- 当前阶段失败且无法通过重试解决时，回滚当前阶段已升级的组件
- 将当前阶段所有组件回滚到阶段起始版本
- 回滚后版本倾斜仍然合规（因为回滚到阶段起始版本，与上一阶段完成状态一致）

**适用场景**：
- 阶段内多个组件升级失败
- 版本不兼容导致的问题
- 需要快速恢复到稳定状态

**执行流程**：
```
阶段 N 失败（重试多次仍失败）
  │
  v
1. 用户触发回滚: kubectl annotate bkecluster <name> bke.bocloud.com/rollback=true
  │
  v
2. 计算回滚目标版本
  ├── 当前阶段: N
  ├── 回滚目标: 阶段 N-1 完成版本（或原始版本如果 N=0）
  └── 例如: 阶段 1 失败，回滚到 1.25（阶段 0 完成版本）
  │
  v
3. 构建回滚 DAG
  ├── 回滚顺序与升级顺序相反
  ├── Worker → Master → etcd → containerd → bkeagent
  └── 每个组件执行降级逻辑
  │
  v
4. 执行回滚 DAG
  ├── 逐节点回滚已升级的组件
  └── 验证版本倾斜合规
  │
  v
5. 更新状态
  ├── CurrentStage = N-1
  ├── StageCompleted[N] = false
  └── ClusterStatus = Ready（回滚完成）
```

**版本倾斜验证**：
```
回滚前: Master = 1.27(部分), Worker = 1.25/1.27(混合)
  倾斜: 1.27 - 1.25 = 2 ✓

回滚中: Master = 1.25(回滚中), Worker = 1.25
  倾斜: 0 ✓

回滚后: Master = 1.25, Worker = 1.25
  倾斜: 0 ✓
```

#### 7.2.3 策略三：跨阶段回退

**设计思路**：
- 当前阶段失败且阶段内回滚也失败时，回退到上一阶段完成状态
- 需要执行完整的降级 DAG，将当前阶段所有已升级组件降级
- 回退后版本倾斜仍然合规（因为回退到上一阶段完成版本）

**适用场景**：
- 阶段内回滚失败
- 需要快速恢复到上一阶段的稳定状态
- 当前阶段存在根本性问题

**执行流程**：
```
阶段 N 回滚失败
  │
  v
1. 用户触发跨阶段回退
  │
  v
2. 计算回退目标版本
  ├── 当前阶段: N
  ├── 回退目标: 阶段 N-1 完成版本
  └── 例如: 阶段 2（1.29）失败，回退到阶段 1 完成版本（1.27）
  │
  v
3. 构建降级 DAG
  ├── 目标版本: 阶段 N-1 的 ReleaseImage Bundle
  └── 降级顺序: Worker → Master → etcd → ...
  │
  v
4. 执行降级 DAG
  ├── 将所有已升级组件降级到阶段 N-1 版本
  └── 验证版本倾斜合规
  │
  v
5. 更新状态
  ├── CurrentStage = N-1
  ├── StageCompleted[N] = false
  ├── StageCompleted[N-1] = true
  └── ClusterStatus = Ready
```

#### 7.2.4 策略对比与选择

| 策略 | 实现复杂度 | 回滚时间 | 数据影响 | 适用场景 |
|------|-----------|---------|---------|---------|
| **阶段内重试** | 低 | 快 | 无 | 瞬时故障、部分节点失败 |
| **阶段内回滚** | 中 | 中 | 小 | 阶段内多组件失败、版本不兼容 |
| **跨阶段回退** | 高 | 慢 | 中 | 阶段内回滚失败、根本性问题 |

**推荐策略**：
1. **首选**：阶段内重试（利用现有断点续传）
2. **次选**：阶段内回滚（回滚当前阶段）
3. **最后**：跨阶段回退（回退到上一阶段）

### 7.3 回滚流程设计

#### 7.3.1 阶段内回滚流程

```
阶段 N 失败（重试 3 次仍失败）
  │
  v
1. 用户触发回滚
  kubectl annotate bkecluster <name> bke.bocloud.com/rollback=stage
  │
  v
2. BKEClusterReconciler 检测回滚注解
  │
  v
3. 计算回滚目标
  ├── 当前阶段: N
  ├── 已完成组件: DeclarativeUpgradeStatus.Completed
  └── 回滚目标: 阶段 N 起始版本（= 阶段 N-1 完成版本）
  │
  v
4. 构建回滚 DAG
  ├── 组件列表: 从 Completed 中提取已升级的组件
  ├── 回滚顺序: 与升级顺序相反
  │   Worker → Master → etcd → containerd → bkeagent
  └── 每个组件的 Target = 阶段 N 起始版本
  │
  v
5. 执行回滚 DAG
  ├── 逐组件执行降级
  │   ├── Worker: drain → 降级 kubelet → 验证
  │   ├── Master: 降级 API Server + kubelet → 验证
  │   ├── etcd: 逐节点降级 → 验证集群健康
  │   └── ...
  └── 每组件完成后验证版本倾斜
  │
  v
6. 回滚完成
  ├── 清除回滚注解
  ├── 更新 DeclarativeUpgradeStatus
  │   ├── CurrentStage = N-1
  │   ├── StageCompleted[N] = false
  │   └── Completed = []（清空当前阶段已完成列表）
  └── ClusterStatus = Ready
```

#### 7.3.2 跨阶段回退流程

```
阶段 N 回滚失败
  │
  v
1. 用户触发跨阶段回退
  kubectl annotate bkecluster <name> bke.bocloud.com/rollback=full
  │
  v
2. BKEClusterReconciler 检测回滚注解
  │
  v
3. 计算回退目标
  ├── 当前阶段: N
  ├── 回退目标: 阶段 N-1 完成版本
  └── 例如: 阶段 2（1.29）→ 阶段 1 完成版本（1.27）
  │
  v
4. 获取回退目标的 ReleaseImage Bundle
  ├── 解析阶段 N-1 的 ReleaseImage
  └── 提取所有组件的目标版本
  │
  v
5. 构建降级 DAG
  ├── 组件列表: 所有已升级的组件
  ├── 降级顺序: 与升级顺序相反
  └── 每个组件的 Target = 阶段 N-1 版本
  │
  v
6. 执行降级 DAG
  ├── 逐组件执行降级
  └── 每组件完成后验证版本倾斜
  │
  v
7. 回退完成
  ├── 清除回滚注解
  ├── 更新 DeclarativeUpgradeStatus
  │   ├── CurrentStage = N-1
  │   ├── StageCompleted[N] = false
  │   ├── StageCompleted[N-1] = true
  │   └── Completed = [阶段 N-1 已完成组件]
  └── ClusterStatus = Ready
```

#### 7.3.3 回滚状态机

```
                    ┌─────────────┐
                    │   Ready     │
                    └──────┬──────┘
                           │ 升级失败
                           v
                    ┌─────────────┐
                    │  Failed     │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              v            v            v
        ┌─────────┐  ┌─────────┐  ┌─────────┐
        │ 重试    │  │阶段内   │  │跨阶段   │
        │         │  │回滚     │  │回退     │
        └────┬────┘  └────┬────┘  └────┬────┘
             │            │            │
             v            v            v
        ┌─────────┐  ┌─────────┐  ┌─────────┐
        │继续升级 │  │Rolling  │  │Rolling  │
        │         │  │Back     │  │BackFull │
        └────┬────┘  └────┬────┘  └────┬────┘
             │            │            │
        ┌────┴────┐       │            │
        │         │       v            v
        v         v   ┌─────────┐  ┌─────────┐
   ┌────────┐ ┌────────┐ │Ready    │  │Ready    │
   │ Ready  │ │ Failed │ │(阶段N-1)│  │(阶段N-1)│
   └────────┘ └────────┘ └─────────┘  └─────────┘
```

### 7.4 回滚状态管理

#### 7.4.1 DeclarativeUpgradeStatus 扩展

```go
// api/bkecommon/v1beta1/bkecluster_status.go 扩展

type DeclarativeUpgradeStatus struct {
    TargetVersion string
    StartedAt     *metav1.Time
    FinishedAt    *metav1.Time
    LastError     string
    LastFailure   *DeclarativeUpgradeFailureRecord
    Completed     []DeclarativeUpgradeComponentRecord

    // 分阶段升级信息
    CurrentStage     int
    TotalStages      int
    StageVersions    []string
    StageCompleted   []bool

    // 新增：回滚信息
    Rollback          *RollbackStatus  // 当前回滚状态
    RollbackHistory   []RollbackRecord // 回滚历史记录
}

type RollbackStatus struct {
    Type           RollbackType   // 回滚类型: Stage/Full
    StartedAt      *metav1.Time
    FinishedAt     *metav1.Time
    TargetStage    int            // 回滚目标阶段
    TargetVersion  string         // 回滚目标版本
    Status         RollbackPhase  // 回滚阶段: Running/Succeeded/Failed
    LastError      string
    Completed      []DeclarativeUpgradeComponentRecord // 已回滚的组件
}

type RollbackType string

const (
    RollbackTypeStage RollbackType = "Stage" // 阶段内回滚
    RollbackTypeFull  RollbackType = "Full"  // 跨阶段回退
)

type RollbackPhase string

const (
    RollbackPhaseRunning   RollbackPhase = "Running"
    RollbackPhaseSucceeded RollbackPhase = "Succeeded"
    RollbackPhaseFailed    RollbackPhase = "Failed"
)

type RollbackRecord struct {
    Type          RollbackType
    FromStage     int
    ToStage       int
    FromVersion   string
    ToVersion     string
    StartedAt     *metav1.Time
    FinishedAt    *metav1.Time
    Status        RollbackPhase
    Reason        string
}
```

#### 7.4.2 回滚进度持久化

```go
// 回滚过程中持久化进度

func (r *BKEClusterReconciler) updateRollbackProgress(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    component string,
    version string,
    succeeded bool,
) error {
    status := &bkeCluster.Status.DeclarativeUpgrade.Rollback

    if succeeded {
        status.Completed = append(status.Completed, DeclarativeUpgradeComponentRecord{
            Name:        component,
            Version:     version,
            CompletedAt: &metav1.Time{Time: time.Now()},
        })
    }

    return r.Status().Update(ctx, bkeCluster)
}
```

#### 7.4.3 回滚断点续传

```go
// 回滚失败后重试，从断点继续

func (r *BKEClusterReconciler) executeRollbackDAG(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    rollbackDAG *topology.UpgradeDAG,
) error {
    status := bkeCluster.Status.DeclarativeUpgrade.Rollback

    // 构建已完成回滚的组件集合
    rolledBack := make(map[string]bool)
    for _, record := range status.Completed {
        rolledBack[record.Name] = true
    }

    // 执行回滚 DAG，跳过已回滚的组件
    batches, _ := rollbackDAG.TopologicalBatches()
    for _, batch := range batches {
        for _, componentName := range batch {
            if rolledBack[componentName] {
                continue // 跳过已回滚的组件
            }
            // 执行组件回滚...
        }
    }
}
```

### 7.5 回滚代码实现

#### 7.5.1 executeRollbackDAG 设计

```go
// controllers/capbke/bkecluster_upgrade_dag.go 新增

func (r *BKEClusterReconciler) executeRollbackDAG(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    rollbackType RollbackType,
) (ctrl.Result, error) {
    status := &bkeCluster.Status.DeclarativeUpgrade

    // 1. 确定回滚目标
    var targetStage int
    var targetVersion string
    switch rollbackType {
    case RollbackTypeStage:
        // 阶段内回滚：回滚到当前阶段起始版本
        targetStage = status.CurrentStage - 1
        if targetStage < 0 {
            targetVersion = status.StageVersions[0] // 回滚到原始版本
        } else {
            targetVersion = status.StageVersions[targetStage]
        }
    case RollbackTypeFull:
        // 跨阶段回退：回退到上一阶段完成版本
        targetStage = status.CurrentStage - 1
        targetVersion = status.StageVersions[targetStage]
    }

    // 2. 获取目标版本的 ReleaseImage Bundle
    targetBundle, err := r.resolveK8sVersionBundle(ctx, targetVersion)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 3. 构建回滚 VersionContext
    rollbackVC := upgrade.BuildVersionContextForUpgrade(targetBundle, currentBundle, bkeCluster)

    // 4. 构建回滚 DAG
    //    回滚 DAG 的组件顺序与升级 DAG 相反
    rollbackDAG, err := r.buildRollbackDAG(targetBundle, status)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 5. 初始化回滚状态
    status.Rollback = &RollbackStatus{
        Type:          rollbackType,
        StartedAt:     &metav1.Time{Time: time.Now()},
        TargetStage:   targetStage,
        TargetVersion: targetVersion,
        Status:        RollbackPhaseRunning,
    }

    // 6. 执行回滚 DAG
    sched := dagexec.NewScheduler(dagexec.Config{...})
    if err := sched.ExecuteDAG(ctx, phaseCtx, oldCluster, newCluster, rollbackDAG); err != nil {
        status.Rollback.Status = RollbackPhaseFailed
        status.Rollback.LastError = err.Error()
        return ctrl.Result{}, err
    }

    // 7. 回滚完成
    status.Rollback.Status = RollbackPhaseSucceeded
    status.Rollback.FinishedAt = &metav1.Time{Time: time.Now()}
    status.CurrentStage = targetStage

    // 记录回滚历史
    status.RollbackHistory = append(status.RollbackHistory, RollbackRecord{
        Type:        rollbackType,
        FromStage:   status.CurrentStage + 1,
        ToStage:     targetStage,
        FromVersion: status.StageVersions[status.CurrentStage+1],
        ToVersion:   targetVersion,
        StartedAt:   status.Rollback.StartedAt,
        FinishedAt:  status.Rollback.FinishedAt,
        Status:      RollbackPhaseSucceeded,
    })

    return r.completeRollback(ctx, bkeCluster)
}
```

#### 7.5.2 回滚阶段计算算法

```go
// pkg/upgrade/version_skew.go 新增

// ComputeRollbackStages 计算回滚的阶段列表
// 从当前阶段回滚到目标阶段
func ComputeRollbackStages(currentStage, targetStage int, stageVersions []string) []string {
    if currentStage <= targetStage {
        return nil // 无需回滚
    }

    var rollbackStages []string
    for i := currentStage; i > targetStage; i-- {
        rollbackStages = append(rollbackStages, stageVersions[i-1])
    }
    return rollbackStages
}

// 示例:
// currentStage = 2, targetStage = 0, stageVersions = ["1.25", "1.27", "1.29"]
// 返回: ["1.27", "1.25"]（从 1.29 回滚到 1.27，再到 1.25）
```

#### 7.5.3 回滚版本倾斜保护

```go
// pkg/phaseframe/phases/version_skew_check.go 新增

// checkRollbackVersionSkew 检查回滚过程中的版本倾斜
// 回滚时同样需要保证版本倾斜合规
func checkRollbackVersionSkew(
    ctx context.Context,
    client client.Client,
    bc *bkev1beta1.BKECluster,
    targetVersion string,
) error {
    // 回滚时版本倾斜检查与升级时相同
    // 确保回滚目标版本与当前 Worker kubelet 版本的倾斜 <= MaxK8sVersionSkew
    return checkVersionSkewCompliance(ctx, client, bc)
}
```

### 7.6 回滚流程图

```
openFuyao 26.03 → 26.06 (K8s 1.25 → 1.29)
  │
  v
阶段 1: K8s 1.25 → 1.27
  │
  ├── Master 节点升级成功 ✓
  ├── Worker 节点 1 升级成功 ✓
  ├── Worker 节点 2 升级失败 ✗
  │
  v
重试阶段 1（3 次）
  │
  ├── Worker 节点 2 仍然失败 ✗
  │
  v
用户触发阶段内回滚
  kubectl annotate bkecluster <name> bke.bocloud.com/rollback=stage
  │
  v
构建回滚 DAG
  ├── 目标版本: 1.25（阶段 1 起始版本）
  ├── 回滚顺序: Worker 节点 1 → Master 节点
  │
  v
执行回滚 DAG
  │
  ├── Worker 节点 1: 降级 kubelet 1.27 → 1.25 ✓
  │   版本倾斜: API Server(1.27) - Worker(1.25) = 2 ✓
  │
  ├── Master 节点: 降级 API Server + kubelet 1.27 → 1.25 ✓
  │   版本倾斜: 0 ✓
  │
  v
回滚完成
  ├── CurrentStage = 0
  ├── StageCompleted[1] = false
  ├── ClusterStatus = Ready
  └── 所有组件版本 = 1.25 ✓
  │
  v
用户修复问题后，重新触发升级
  kubectl annotate bkecluster <name> bke.bocloud.com/rollback-
  kubectl patch clusterversion --type merge -p '{"spec":{"desiredVersion":"v26.06"}}'
  │
  v
从阶段 1 重新开始升级...
```

## 八、关键设计决策

### 8.1 分阶段升级决策

| 决策 | 选择 | 理由 |
|------|------|------|
| **中间版本计算** | 自动计算（基于 maxSkew=2） | 无需用户配置，自动适配 |
| **分阶段粒度** | 每个中间版本一个完整 DAG | 阶段间有明确的版本倾斜验证点 |
| **Master 节点升级** | API Server + kubelet 一起升级 | 与现有实现一致（`UpgradeControlPlane`） |
| **阶段间等待** | 等待所有节点完成当前阶段 | 确保版本倾斜合规后再进入下一阶段 |
| **断点续传** | 基于 `StageCompleted` 数组 | 失败后从当前阶段重试 |
| **中间版本 Bundle** | 通过 ReleaseImage CR 查找 | 复用现有 OCI Bundle 机制 |

### 8.2 回滚设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| **回滚策略优先级** | 阶段内重试 → 阶段内回滚 → 跨阶段回退 | 渐进式回滚，最小化影响范围 |
| **回滚触发方式** | 手动触发（kubectl annotate） | 避免自动回滚掩盖问题，给用户决策权 |
| **回滚粒度** | 以阶段为单位 | 与分阶段升级对齐，版本倾斜可控 |
| **回滚顺序** | 与升级顺序相反（Worker → Master → etcd） | 遵循 Kubernetes 组件依赖关系 |
| **回滚断点续传** | 基于 `RollbackStatus.Completed` | 回滚失败后可从断点继续 |
| **回滚后状态** | 回滚到阶段起始版本，ClusterStatus = Ready | 确保集群处于稳定状态，可重新发起升级 |

## 九、代码改造清单

### 9.1 分阶段升级改造

| 文件 | 改造内容 | 工作量 |
|------|---------|--------|
| `pkg/upgrade/version_skew.go`（新增） | 版本倾斜检查、中间版本计算 | 0.5 人月 |
| `controllers/capbke/bkecluster_upgrade_dag.go` | 多 DAG 分阶段执行 | 1.0 人月 |
| `api/bkecommon/v1beta1/bkecluster_status.go` | DeclarativeUpgradeStatus 扩展（阶段字段） | 0.3 人月 |
| `pkg/phaseframe/phases/version_skew_check.go`（新增） | 运行时版本倾斜检查 | 0.3 人月 |
| `pkg/phaseframe/phases/ensure_master_upgrade.go` | 集成版本倾斜预检 | 0.2 人月 |
| `pkg/phaseframe/phases/ensure_worker_upgrade.go` | 集成版本倾斜预检 | 0.2 人月 |
| 分阶段升级测试 | 单元测试、集成测试 | 0.5 人月 |
| **小计** | | **3.0 人月** |

### 9.2 回滚能力改造

| 文件 | 改造内容 | 工作量 |
|------|---------|--------|
| `controllers/capbke/bkecluster_upgrade_dag.go` | executeRollbackDAG 实现 | 0.5 人月 |
| `api/bkecommon/v1beta1/bkecluster_status.go` | DeclarativeUpgradeStatus 扩展（回滚字段） | 0.3 人月 |
| `pkg/upgrade/version_skew.go` | ComputeRollbackStages 实现 | 0.2 人月 |
| `pkg/phaseframe/phases/version_skew_check.go` | 回滚版本倾斜检查 | 0.1 人月 |
| 回滚测试 | 单元测试、集成测试、端到端测试 | 0.3 人月 |
| **小计** | | **1.4 人月** |

### 9.3 总计

| 类别 | 工作量 |
|------|--------|
| 分阶段升级改造 | 3.0 人月 |
| 回滚能力改造 | 1.4 人月 |
| **总计** | **4.4 人月** |

---

**文档版本**：v1.0
**创建日期**：2026-08-10
**基于代码版本**：cluster-api-provider-bke main 分支

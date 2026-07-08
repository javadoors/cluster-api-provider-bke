# KEP-10: 控制面性能瓶颈治理与资源访问优化方案

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-10 |
| **标题** | 控制面性能瓶颈治理与资源访问优化方案 |
| **状态** | `provisional` |
| **类型** | Enhancement |
| **作者** | openFuyao Team |
| **创建日期** | 2026-07-07 |
| **依赖** | KEP-5（PhaseFrame）、KEP-6（声明式组件管理） |
| **相关** | KEPU-2（ActionEngineEnhancement） |

## 1. 摘要

本提案针对 cluster-api-provider-bke 工程在性能压测与大规模集群（≥100 节点）场景中暴露出的控制面性能瓶颈，提出系统性的资源访问优化方案。通过对代码的走读分析，识别出 7 类核心瓶颈：远程集群客户端无缓存、状态同步流程内的重复 GET、命令完成等待的固定 2s 轮询、冲突重试风暴、master join 的 N 次单点查询、env 脚本串行下发、以及 reconcile 周期内的重复 List 节点。

本提案给出 7 个独立的改造方案，每个方案均可独立落地、独立回滚，整体收益预期：API Server QPS 下降 30~50%，`BKECluster` reconcile P99 时延下降 40%+，`Command` CR 的 GET 次数与命令数比从 ≥30:1 降至接近 1:1。

## 2. 动机

### 2.1 现状痛点

| 问题 | 现状代码位置 | 影响 |
|------|-------------|------|
| **远程客户端无缓存** | `pkg/kube/kube.go:158-180` `NewRemoteClientByBKECluster` 每次重建 ClientSet + DynamicClient | 相同 BKECluster 短时间内被多次重建 client，TLS 握手开销累积 |
| **状态同步重复 GET** | `pkg/mergecluster/bkecluster.go:326-389` `updateCombinedBKEClusterWithParams` 内部 3 次 GET | 单次 patch 触发 3 次 API Server 调用，叠加循环放大 |
| **命令等待固定轮询** | `pkg/command/command.go:378-396` `DefaultWaitInterval=2s` 无指数退避 | 大规模节点场景下每命令产生大量空轮询 GET |
| **冲突重试无退避** | `pkg/mergecluster/bkecluster.go:67-75` `IsConflict` 时 `continue` + 强制 `time.Sleep(0~2s)` | 高并发更新时形成冲突风暴；成功路径强制延迟 |
| **master join 单点查询** | `pkg/phaseframe/phases/ensure_master_join.go:303-329` 每节点单独 GET Machine | N 节点 × M 轮询 = N×M 次 Machine 查询 |
| **env 脚本串行下发** | `pkg/phaseframe/phases/ensure_nodes_env.go:445-489` 7 个独立脚本顺序 Wait | 每脚本 2s 轮询串行叠加，节点环境初始化耗时显著拉长 |
| **reconcile 内重复 List** | 各 phase 的 `NeedExecute` 与 `Execute` 各自 `GetBKENodesWrapperForCluster` | 同一周期内 BKENodes 被 List 两次 |

### 2.2 目标

1. **客户端复用**：为远程集群客户端引入按 cluster key 的缓存层，复用 ClientSet/DynamicClient，TTL 过期自动失效。
2. **状态同步合并 GET**：将 `UpdateCombinedBKECluster` 单次流程内的 3 次 GET 合并为 1 次 BKECluster GET + 1 次 ConfigMap GET。
3. **命令等待 watch 化**：优先使用 informer watch 监听 Command 状态变更；不可用时降级为指数退避轮询。
4. **冲突退避化**：`SyncStatusUntilComplete` 的冲突重试改为指数退避，删除成功路径的强制 sleep。
5. **master join 批量化**：将 N 次单点 `NodeToMachine` 改为 1 次 List Machine + 指数退避。
6. **env 脚本并发化**：相互独立的 `install-*.sh` 脚本并发下发，前置依赖脚本串行。
7. **节点列表 reconcile 内缓存**：`PhaseContext` 引入 `sync.Once` 缓存节点列表，消除同一周期内的重复 List。

### 2.3 非目标

1. 不重构 DAG 调度器核心逻辑（已使用 `errgroup` + 信号量限流，性能良好）。
2. 不重写 SSH 推送机制（`bkessh.MultiCli` 已并发推送，性能良好）。
3. 不修改 worker join 的状态检查逻辑（已使用 `sync.WaitGroup` 并发，性能良好）。
4. 不引入新的外部依赖（仅使用 `golang.org/x/sync/singleflight`、`k8s.io/apimachinery/pkg/util/wait` 等已有库）。
5. 不变更任何 CRD 的 Spec 字段定义，保证向后兼容。

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| **ClientCache** | 新增 `pkg/kube/clientcache.go`，按 cluster key 缓存 RemoteKubeClient，支持 TTL 过期与手动失效 |
| **ClusterSnapshot** | 在 `pkg/mergecluster` 内引入快照对象，合并单次 patch 流程的多次 GET |
| **Command Watch** | 改造 `pkg/command/command.go` 的 `waitForCommandCompletion`，watch-first + 指数退避降级 |
| **SyncStatus Backoff** | 改造 `SyncStatusUntilComplete` 使用 `wait.Backoff` 指数退避 |
| **Master Join List** | 改造 `waitForNodesJoin` 使用单次 List Machine + 指数退避 |
| **Env Scripts Concurrent** | 改造 `executeExtraExecScripts` 为前置依赖串行 + 独立脚本并发 |
| **PhaseContext NodeCache** | `PhaseContext` 引入 `sync.Once` 缓存节点列表，修改节点后手动失效 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 所有改造均提供降级路径，缓存失效或 watch 不可用时回退到原逻辑 |
| **线程安全** | ClientCache 使用 `sync.RWMutex` + `singleflight` 防止缓存击穿 |
| **生命周期管理** | ClientCache 必须在 controller 启动时初始化，cluster 删除/token 轮换时失效对应条目 |
| **观测性** | 改造点必须暴露指标（缓存命中率、watch 降级率、backoff 次数），便于回归验证 |
| **不破坏幂等性** | 所有改造不得改变现有 reconcile 的幂等语义，仅优化资源访问方式 |
| **测试覆盖** | 每个方案必须配套单测，覆盖缓存命中/失效、watch 降级、并发脚本等场景 |

## 4. 提案设计

### 4.1 整体架构

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          Management Cluster                                 │
│                                                                             │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                    BKECluster Controller                              │  │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────────┐  │  │
│  │  │  PhaseContext    │  │  ClientCache    │  │  ClusterSnapshot    │  │  │
│  │  │  ├ nodeCacheOnce │  │  ├ sync.RWMutex │  │  ├ BKECluster       │  │  │
│  │  │  ├ nodeCache     │  │  ├ items map    │  │  ├ ConfigMap        │  │  │
│  │  │  └ Invalidate    │  │  ├ singleflight │  │  └ patchHelper      │  │  │
│  │  └─────────────────┘  │  └ Invalidate    │  └─────────────────────┘  │  │
│  │                        └─────────────────┘                            │  │
│  │  ┌─────────────────────────────────────────────────────────────────┐ │  │
│  │  │                     Phase Pipeline                               │ │  │
│  │  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐  │ │  │
│  │  │  │ EnsureNodes  │  │ EnsureMaster │  │ EnsureContainerd...   │  │ │  │
│  │  │  │ Env          │  │ Init/Join    │  │                       │  │ │  │
│  │  │  │ ├ scripts    │  │ ├ List Mach  │  │                       │  │ │  │
│  │  │  │ │ concurrent  │  │ │ + backoff  │  │                       │  │ │  │
│  │  │  │ └ Wait()     │  │ └ wait.Backoff│  │                       │  │ │  │
│  │  │  └──────────────┘  └──────────────┘  └──────────────────────┘  │ │  │
│  │  └─────────────────────────────────────────────────────────────────┘ │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
│                                             │                               │
│                                             ▼                               │
│  ┌───────────────────────────────────────────────────────────────────────┐  │
│  │                        API Server                                      │  │
│  │   QPS 下降 30~50% ←── 合并 GET / 缓存 / watch / 指数退避               │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 方案 1：远程集群客户端缓存（ClientCache）

#### 4.2.1 设计思路

在 `pkg/kube` 包中引入 `ClientCache`，按 `types.NamespacedName` 缓存 `RemoteKubeClient`。使用 `sync.RWMutex` 保护并发读写，`golang.org/x/sync/singleflight` 合并同 key 并发重建请求，防止缓存击穿。

#### 4.2.2 数据结构

```go
// pkg/kube/clientcache.go
package kube

const defaultClientTTL = 10 * time.Minute

type ClientCache struct {
    localClient client.Client
    ttl         time.Duration

    mu    sync.RWMutex
    items map[types.NamespacedName]*cacheEntry

    sf singleflight.Group
}

type cacheEntry struct {
    remote    RemoteKubeClient
    expiresAt time.Time
}
```

#### 4.2.3 核心方法

```go
// GetClientFromCache 命中即返回；未命中则通过 builder 构建并缓存
func (c *ClientCache) GetClientFromCache(
    ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
) (RemoteKubeClient, error) {
    key := types.NamespacedName{Namespace: bkeCluster.Namespace, Name: bkeCluster.Name}

    // fast path：读锁命中且未过期
    c.mu.RLock()
    if entry, ok := c.items[key]; ok && time.Now().Before(entry.expiresAt) {
        c.mu.RUnlock()
        return entry.remote, nil
    }
    c.mu.RUnlock()

    // singleflight 合并同 key 并发请求
    v, err, _ := c.sf.Do(key.String(), func() (interface{}, error) {
        remote, err := NewRemoteClientByBKECluster(ctx, c.localClient, bkeCluster)
        if err != nil {
            return nil, err
        }
        c.mu.Lock()
        c.items[key] = &cacheEntry{remote: remote, expiresAt: time.Now().Add(c.ttl)}
        c.mu.Unlock()
        return remote, nil
    })
    if err != nil {
        return nil, err
    }
    return v.(RemoteKubeClient), nil
}

// Invalidate 失效某个 cluster 的缓存
func (c *ClientCache) Invalidate(namespace, name string) {
    key := types.NamespacedName{Namespace: namespace, Name: name}
    c.mu.Lock()
    delete(c.items, key)
    c.mu.Unlock()
}
```

#### 4.2.4 改造 `GetTargetClusterClient`

```go
// pkg/kube/kube.go
var globalClientCache *ClientCache

func InitClientCache(localClient client.Client) {
    globalClientCache = NewClientCache(localClient, defaultClientTTL)
}

func GetTargetClusterClient(
    ctx context.Context,
    c client.Client,
    bkeCluster *bkev1beta1.BKECluster,
) (*kubernetes.Clientset, dynamic.Interface, error) {
    // 优先走缓存
    if globalClientCache != nil {
        remote, err := globalClientCache.GetClientFromCache(ctx, bkeCluster)
        if err == nil {
            cs, dc := remote.KubeClient()
            return cs, dc, nil
        }
        // 缓存失败回退到原路径
    }
    // 原有逻辑作为兜底
    cluster, err := util.GetOwnerCluster(ctx, c, bkeCluster.ObjectMeta)
    if err != nil {
        return nil, nil, errors.Wrapf(err, "failed to get owner cluster for bkeCluster %q", bkeCluster.Name)
    }
    remoteClient, err := NewRemoteClientByCluster(ctx, c, cluster)
    if err != nil {
        return nil, nil, errors.Wrapf(err, "failed to create remote cluster %q client", cluster.Name)
    }
    cs, dc := remoteClient.KubeClient()
    return cs, dc, nil
}
```

#### 4.2.5 生命周期管理

| 时机 | 动作 |
|------|------|
| Controller 启动 | `cmd/manager/main.go` 调用 `kube.InitClientCache(mgr.GetClient())` |
| BKECluster 删除 | Reconciler 调用 `globalClientCache.Invalidate(ns, name)` |
| Token 轮换 | Token 轮换逻辑触发 `Invalidate` |
| TTL 过期 | 自动失效，下次访问重建 |

### 4.3 方案 2：合并 `UpdateCombinedBKECluster` 内的多次 GET（ClusterSnapshot）

#### 4.3.1 设计思路

引入 `ClusterSnapshot` 结构，一次 GET 后在 patch 流程内复用，将原 3 次 GET（BKECluster + ConfigMap + BKECluster）合并为 1 次 BKECluster GET + 1 次 ConfigMap GET。

#### 4.3.2 数据结构

```go
// pkg/mergecluster/bkecluster.go

type ClusterSnapshot struct {
    BKECluster  *v1beta1.BKECluster
    ConfigMap   *corev1.ConfigMap
    patchHelper *patch.Helper
}
```

#### 4.3.3 加载与复用

```go
// LoadClusterSnapshot 一次性加载 BKECluster + ConfigMap，构建 patch helper
func LoadClusterSnapshot(
    ctx context.Context,
    c client.Client,
    key types.NamespacedName,
) (*ClusterSnapshot, error) {
    bkeCluster := &v1beta1.BKECluster{}
    if err := c.Get(ctx, key, bkeCluster); err != nil {
        return nil, err
    }
    cm, err := GetCombinedBKEClusterCM(ctx, c, bkeCluster)
    if err != nil {
        return nil, err
    }
    patchHelper, err := patch.NewHelper(bkeCluster, c)
    if err != nil {
        return nil, err
    }
    return &ClusterSnapshot{
        BKECluster:  bkeCluster,
        ConfigMap:   cm,
        patchHelper: patchHelper,
    }, nil
}

// updateCombinedBKEClusterWithParams 重构后只使用 snapshot 内的对象
func updateCombinedBKEClusterWithParams(params UpdateCombinedBKEClusterParams) error {
    key := types.NamespacedName{
        Namespace: params.CombinedCluster.Namespace,
        Name:      params.CombinedCluster.Name,
    }
    snap, err := LoadClusterSnapshot(params.Ctx, params.Client, key)
    if err != nil {
        return err
    }

    // 复用 snap 内对象，不再重复 GET
    currentBkeCluster := snap.BKECluster
    patchHelper := snap.patchHelper
    cm := snap.ConfigMap

    // 从 cm 解析 nodesCM，不再调用 getBkeClusterAssociateNodesCM
    nodesCM := &bkeNodes{}
    if v, ok := cm.Data["nodes"]; ok {
        _ = json.Unmarshal([]byte(v), &nodesCM.spec)
    }

    // handleExternalUpdates / processNodeData / updateClusterAndConfigMap
    // 改为接收 snap 内对象
    // ...
    return nil
}
```

#### 4.3.4 收益对比

| 指标 | 改造前 | 改造后 |
|------|--------|--------|
| 单次 patch 的 BKECluster GET | 2 次（prepareClusterData + initializePatchHelper） | 1 次 |
| 单次 patch 的 ConfigMap GET | 2 次（GetCombinedBKEClusterCM + getBkeClusterAssociateNodesCM） | 1 次 |
| 总 API 调用 | 4 次 | 2 次 |

### 4.4 方案 3：命令完成等待改为 watch + 指数退避

#### 4.4.1 设计思路

优先使用 controller-runtime client 的 Watch 能力监听 Command 状态变更；watch 不可用时降级为指数退避轮询（替代固定 2s）。

#### 4.4.2 改造 `waitForCommandCompletion`

```go
// pkg/command/command.go

func (b *BaseCommand) waitForCommandCompletion(
    complete *bool,
    successNodes, failedNodes *[]string,
) error {
    ctxTimeout, cancel := context.WithTimeout(b.Ctx, b.WaitTimeout)
    defer cancel()

    // 优先尝试 watch
    if err := b.watchCommandComplete(ctxTimeout, complete, successNodes, failedNodes); err == nil {
        return nil
    }
    // 降级到指数退避轮询
    return b.pollCommandCompleteWithBackoff(ctxTimeout, complete, successNodes, failedNodes)
}
```

#### 4.4.3 watch 实现

```go
func (b *BaseCommand) watchCommandComplete(
    ctx context.Context,
    complete *bool,
    successNodes, failedNodes *[]string,
) error {
    watcher, err := b.Client.Watch(ctx, &agentv1beta1.CommandList{},
        client.InNamespace(b.commandNS),
        client.MatchingFields{"metadata.name": b.commandName})
    if err != nil {
        return err
    }
    defer watcher.Stop()

    for {
        select {
        case <-ctx.Done():
            return wait.ErrWaitTimeout
        case event, ok := <-watcher.ResultChan():
            if !ok {
                return errors.New("watch channel closed")
            }
            cmd, ok := event.Object.(*agentv1beta1.Command)
            if !ok {
                continue
            }
            *complete, *successNodes, *failedNodes = CheckCommandStatus(cmd)
            if *complete {
                return nil
            }
        }
    }
}
```

#### 4.4.4 指数退避降级

```go
func (b *BaseCommand) pollCommandCompleteWithBackoff(
    ctx context.Context,
    complete *bool,
    successNodes, failedNodes *[]string,
) error {
    backoff := wait.Backoff{
        Duration: 2 * time.Second,
        Factor:   1.5,
        Jitter:   0.2,
        Steps:    20,
        Cap:      15 * time.Second,
    }
    return wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
        command, err := b.GetCommand()
        if err != nil {
            return false, nil
        }
        *complete, *successNodes, *failedNodes = CheckCommandStatus(command)
        return *complete, nil
    })
}
```

#### 4.4.5 前置条件

| 前置条件 | 说明 |
|----------|------|
| Command CRD 注册 informer | controller 启动时为 `agentv1beta1.Command` 注册 informer，否则 watch 路径不可用，自动降级 |
| FieldIndexer 注册 | 为 `metadata.name` 注册 index，加速 watch 过滤 |

### 4.5 方案 4：`SyncStatusUntilComplete` 引入指数退避

#### 4.5.1 设计思路

将冲突重试的 `continue` 改为指数退避，删除成功路径的强制 `time.Sleep(0~2s)`，适当延长总超时以配合 backoff。

#### 4.5.2 改造实现

```go
// pkg/mergecluster/bkecluster.go

const (
    SyncStatusTimeout = 5 * time.Minute // 适当延长，配合 backoff
)

func SyncStatusUntilComplete(
    c client.Client,
    bkeCluster *v1beta1.BKECluster,
    patchs ...PatchFunc,
) (err error) {
    log := log.With("name", "syncer")
    ctx, cancel := context.WithTimeout(context.Background(), SyncStatusTimeout)
    defer cancel()

    backoff := wait.Backoff{
        Duration: 100 * time.Millisecond,
        Factor:   2.0,
        Jitter:   0.3,
        Steps:    10,
        Cap:      5 * time.Second,
    }

    lastErr := errors.New("init")
    err = wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
        err := UpdateCombinedBKECluster(ctx, c, bkeCluster, []string{}, patchs...)
        if err == nil {
            return true, nil
        }
        if apierrors.IsNotFound(err) {
            log.Warnf("bkeCluster %q not found, skip update", utils.ClientObjNS(bkeCluster))
            return true, nil
        }
        if apierrors.IsConflict(err) {
            lastErr = err
            return false, nil // 退避重试
        }
        if apierrors.IsForbidden(err) || apierrors.IsBadRequest(err) || apierrors.IsInvalid(err) {
            return false, err
        }
        lastErr = err
        return false, nil
    })
    if err == nil {
        return nil
    }
    if errors.Is(err, wait.ErrWaitTimeout) {
        return errors.Wrap(lastErr, "sync status timeout after backoff")
    }
    return err
}
```

#### 4.5.3 行为对比

| 场景 | 改造前 | 改造后 |
|------|--------|--------|
| 成功路径 | 强制 sleep 0~2s | 立即返回 |
| 冲突重试 | `continue` 无退避 | 100ms → 5s 指数退避 |
| 总超时 | 2min | 5min（配合退避步数） |

### 4.6 方案 5：master join 轮询改为 List + 指数退避

#### 4.6.1 设计思路

将 N 次单点 `NodeToMachine` GET 改为 1 次 List Machine（带 cluster label），配合指数退避降低轮询频率。

#### 4.6.2 改造实现

```go
// pkg/phaseframe/phases/ensure_master_join.go

func waitForNodesJoin(params WaitForNodesJoinParams) error {
    pollCount := 0
    backoff := wait.Backoff{
        Duration: 2 * time.Second,
        Factor:   1.5,
        Jitter:   0.2,
        Steps:    30,
        Cap:      10 * time.Second,
    }

    // 预先把待加入节点 IP 集合化
    pending := make(map[string]int, len(params.NodesToJoin))
    for i, node := range params.NodesToJoin {
        pending[node.IP] = i
    }

    err := wait.ExponentialBackoffWithContext(params.Timeout, backoff, func(ctx context.Context) (bool, error) {
        pollCount++

        // 一次性 List 所有 Machine（带 cluster label）
        machineList := &clusterv1.MachineList{}
        listErr := params.Client.List(ctx, machineList,
            client.InNamespace(params.BKECluster.Namespace),
            client.MatchingLabels{clusterv1.ClusterLabelName: params.BKECluster.Name})
        if listErr != nil {
            return false, nil
        }

        // 构建 IP -> NodeRef 索引
        for _, m := range machineList.Items {
            if m.Status.NodeRef == nil {
                continue
            }
            ip, ok := m.Labels[bkev1beta1.NodeIPLabel]
            if !ok {
                continue
            }
            if idx, ok := pending[ip]; ok {
                params.Log.Info(constant.MasterJoinSucceedReason,
                    "Master node join success. node: %v",
                    phaseutil.NodeInfo(params.NodesToJoin[idx]))
                params.SuccessJoinNode[idx] = params.NodesToJoin[idx]
                delete(pending, ip)
            }
        }

        if len(pending) == 0 {
            params.Log.Info(constant.MasterJoiningReason,
                "All master joined, total: %d", len(params.NodesToJoin))
            return true, nil
        }
        if pollCount%LogOutputInterval == 0 {
            params.Log.Info(constant.MasterJoiningReason,
                "Wait master join. success: %d, pending: %d",
                len(params.SuccessJoinNode), len(pending))
        }
        return false, nil
    })
    return err
}
```

#### 4.6.3 前置条件

| 前置条件 | 说明 |
|----------|------|
| Machine 关联节点 IP | Machine 必须通过 label（如 `bkev1beta1.NodeIPLabel`）携带节点 IP，否则无法关联 |

#### 4.6.4 收益对比

| 指标 | 改造前 | 改造后 |
|------|--------|--------|
| 每轮 API 调用 | N 次 GET Machine（N = 待加入节点数） | 1 次 List Machine |
| 轮询间隔 | 固定 1s | 2s → 10s 指数退避 |
| 10 节点 × 30 轮 总调用 | 300 次 | 30 次 |

### 4.7 方案 6：env 脚本并发化

#### 4.7.1 设计思路

将 `install-lxcfs.sh`、`install-nfsutils.sh`、`install-etcdctl.sh`、`install-helm.sh`、`install-calicoctl.sh`、`update-runc.sh`、`clean-docker-images.py` 等 7 个相互独立的脚本并发下发；`file-downloader.sh`、`package-downloader.sh` 作为前置依赖串行执行。

#### 4.7.2 脚本依赖图

```
file-downloader.sh ──┐
                     ├──► install-lxcfs.sh        ─┐
                     ├──► install-nfsutils.sh      │
                     ├──► install-etcdctl.sh        │
                     ├──► install-helm.sh           ├──► 并发执行
                     ├──► install-calicoctl.sh      │
                     ├──► update-runc.sh            │
                     └──► clean-docker-images.py   ─┘
package-downloader.sh ┘
```

#### 4.7.3 改造实现

```go
// pkg/phaseframe/phases/ensure_nodes_env.go

// 脚本依赖关系：file-downloader / package-downloader 是其他脚本的前置
var commonScriptNames = []string{"file-downloader.sh", "package-downloader.sh"}

// executeExtraExecScriptsConcurrent 并发执行独立脚本，前置依赖串行
func (e *EnsureNodesEnv) executeExtraExecScriptsConcurrent(scripts []string) error {
    ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()

    // 阶段 1：串行执行 common 脚本（file-downloader / package-downloader）
    for _, script := range commonScriptNames {
        if !utils.ContainsString(scripts, script) {
            continue
        }
        if err := e.executeSingleScript(ctx, c, bkeCluster, scheme, log, script); err != nil {
            return err
        }
    }

    // 阶段 2：并发执行其他独立脚本
    var rest []string
    for _, s := range scripts {
        if !utils.ContainsString(commonScriptNames, s) {
            rest = append(rest, s)
        }
    }

    g, gctx := errgroup.WithContext(ctx)
    g.SetLimit(4) // 限制并发度，避免节点上 IO 争抢

    for _, script := range rest {
        script := script
        g.Go(func() error {
            return e.executeSingleScript(gctx, c, bkeCluster, scheme, log, script)
        })
    }
    return g.Wait()
}

func (e *EnsureNodesEnv) executeSingleScript(
    ctx context.Context,
    c client.Client,
    cluster *bkev1beta1.BKECluster,
    scheme *runtime.Scheme,
    log *bkev1beta1.BKELogger,
    script string,
) error {
    // 抽取原 installCommonScripts / installOtherCustomScripts 中单脚本逻辑
    // ...
    return nil
}
```

#### 4.7.4 收益对比

| 指标 | 改造前 | 改造后 |
|------|--------|--------|
| 7 脚本总耗时 | 7 × (下发 + 2s × N 轮询) | 2 × (下发 + 轮询) + max(4) × (下发 + 轮询) |
| 并发度 | 1（串行） | 4（限制） |

### 4.8 方案 7：PhaseContext 内缓存 NodeFetcher 结果

#### 4.8.1 设计思路

在 `PhaseContext` 中引入 `sync.Once` 缓存节点列表，同一 reconcile 周期内只 List 一次 BKENodes；修改节点状态的操作执行后手动失效缓存。

#### 4.8.2 数据结构

```go
// pkg/phaseframe/phasecontext.go

type PhaseContext struct {
    // ... 原有字段

    // 节点缓存：reconcile 内有效
    nodeCacheOnce sync.Once
    nodeCache     bkenode.Nodes
    nodeCacheErr  error
}

// GetCachedBKENodes 同一 reconcile 内只 List 一次
func (p *PhaseContext) GetCachedBKENodes(cluster *bkev1beta1.BKECluster) (bkenode.Nodes, error) {
    p.nodeCacheOnce.Do(func() {
        p.nodeCache, p.nodeCacheErr = p.NodeFetcher().GetNodesForBKECluster(p, cluster)
    })
    return p.nodeCache, p.nodeCacheErr
}

// InvalidateNodeCache 在执行修改节点的操作后手动失效
func (p *PhaseContext) InvalidateNodeCache() {
    p.nodeCacheOnce = sync.Once{}
    p.nodeCache = nil
    p.nodeCacheErr = nil
}
```

#### 4.8.3 改造各 phase

```go
// pkg/phaseframe/phases/ensure_nodes_env.go

func (e *EnsureNodesEnv) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
    if !e.BasePhase.DefaultNeedExecute(old, new) {
        return false
    }
    bkeNodes, err := e.Ctx.GetCachedBKENodes(new) // 改为缓存版本
    if err != nil {
        return false
    }
    needExecute := phaseutil.HasNodesNeedingPhase(bkeNodes, bkev1beta1.NodeEnvFlag)
    if needExecute {
        e.SetStatus(bkev1beta1.PhaseWaiting)
    }
    return needExecute
}

func (e *EnsureNodesEnv) getNodesToInitEnv() bkenode.Nodes {
    bkeNodes, err := e.Ctx.GetCachedBKENodes(bkeCluster) // 复用缓存
    // ...
}
```

#### 4.8.4 失效时机

| 时机 | 动作 |
|------|------|
| `SetNodeStateWithMessageForCluster` | 调用后 `InvalidateNodeCache()` |
| `MarkNodeStateFlagForCluster` | 调用后 `InvalidateNodeCache()` |
| `DeleteBKENodeForCluster` | 调用后 `InvalidateNodeCache()` |
| Reconcile 入口 | 重置 `nodeCacheOnce`（新对象自然重置） |

## 5. 实施计划

### 5.1 落地路线图

| 阶段 | 方案 | 优先级 | 风险 | 验证方式 |
|------|------|--------|------|----------|
| P0 | 方案 1（ClientCache） | 高 | 低，有降级 | 单测 + 集成测试验证 client 复用 |
| P0 | 方案 2（ClusterSnapshot） | 高 | 中，影响状态写回 | 重点回归 `SyncStatusUntilComplete` 路径 |
| P0 | 方案 3（Command Watch） | 高 | 中，依赖 informer | 验证降级路径在无 cache 时正常 |
| P1 | 方案 4（SyncStatus Backoff） | 中 | 低 | 单测验证 backoff 行为 |
| P1 | 方案 5（Master Join List） | 中 | 中，依赖 Machine label | 验证 Machine 关联 IP 的方式 |
| P2 | 方案 6（Env Scripts Concurrent） | 中 | 中，需梳理依赖 | 集成测试验证脚本执行顺序 |
| P2 | 方案 7（PhaseContext NodeCache） | 低 | 低 | 单测验证缓存失效逻辑 |

### 5.2 分阶段交付

#### P0 阶段（核心瓶颈治理）

- 涉及方案：1、2、3
- 改造文件：
  - 新增 `pkg/kube/clientcache.go`
  - 修改 `pkg/kube/kube.go`
  - 修改 `pkg/mergecluster/bkecluster.go`
  - 修改 `pkg/command/command.go`
  - 修改 `cmd/manager/main.go`
- 验收标准：
  - API Server QPS 下降 ≥30%
  - `Command` CR GET 次数/命令数比 ≤ 5:1
  - 单测覆盖率 ≥80%

#### P1 阶段（轮询优化）

- 涉及方案：4、5
- 改造文件：
  - 修改 `pkg/mergecluster/bkecluster.go`
  - 修改 `pkg/phaseframe/phases/ensure_master_join.go`
- 验收标准：
  - master join 总耗时下降 ≥40%
  - 冲突重试不再形成风暴（通过指标观测）

#### P2 阶段（并发化与缓存）

- 涉及方案：6、7
- 改造文件：
  - 修改 `pkg/phaseframe/phases/ensure_nodes_env.go`
  - 修改 `pkg/phaseframe/phasecontext.go`（或现有 PhaseContext 定义）
  - 各 phase 的 `NeedExecute` / `Execute` 方法
- 验收标准：
  - env 初始化总耗时下降 ≥50%
  - reconcile 内 BKENodes List 次数 ≤1

## 6. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| ClientCache 缓存过期后并发重建 | 短暂的 client 重建开销 | `singleflight` 合并同 key 并发请求 |
| Command watch 不可用 | 命令完成感知延迟 | 自动降级到指数退避轮询 |
| ClusterSnapshot 内对象被外部修改 | 状态写回冲突 | patch helper 的 conflict 重试机制保留 |
| Machine 未携带 IP label | master join 无法关联节点 | 回退到原 N 次单点查询逻辑 |
| env 脚本并发执行 IO 争抢 | 节点上下载/安装失败 | `g.SetLimit(4)` 限制并发度 |
| PhaseContext 节点缓存脏读 | 使用过期节点数据 | 修改节点操作后立即 `InvalidateNodeCache` |

## 7. 验证指标

### 7.1 性能指标

| 指标 | 基线 | 目标 | 采集方式 |
|------|------|------|----------|
| API Server QPS（100 节点集群） | 待采集 | 下降 30~50% | API Server metrics |
| `BKECluster` reconcile P99 时延 | 待采集 | 下降 40%+ | controller runtime metrics |
| `Command` CR GET 次数/命令数 | ≥30:1 | ≤5:1 | 自定义指标 |
| master join 总耗时（10 节点） | 待采集 | 下降 40%+ | 端到端测试 |
| env 初始化总耗时（100 节点） | 待采集 | 下降 50%+ | 端到端测试 |

### 7.2 功能指标

| 指标 | 目标 | 采集方式 |
|------|------|----------|
| ClientCache 命中率 | ≥90% | 自定义指标 |
| Command watch 成功率 | ≥95% | 自定义指标（降级率反向） |
| 冲突重试次数（单次 sync） | ≤3 | 自定义指标 |

### 7.3 回归验证

| 场景 | 验证点 |
|------|--------|
| 100 节点集群安装 | 全流程通过，无性能退化 |
| 50 节点集群升级 | 升级成功率与时延对比 |
| 集群扩缩容 | master/worker join/delete 正常 |
| token 轮换 | ClientCache 正确失效，无脏 client |
| controller 重启 | ClientCache 重建，无内存泄漏 |

## 8. 设计决策记录

### 8.1 为什么选择 TTL 缓存而不是 LRU

| 维度 | TTL | LRU |
|------|-----|-----|
| 实现复杂度 | 低 | 中 |
| 失效确定性 | 高（时间驱动） | 低（访问驱动） |
| token 轮换适配 | 好（TTL 兜底） | 差（需手动失效） |
| 内存占用 | 可控（TTL 内固定） | 不可控（依赖访问模式） |

决策：选择 TTL，因为 token 轮换有明确的时间窗口，TTL 可作为兜底失效机制。

### 8.2 为什么 watch 优先而不是纯指数退避

| 维度 | watch | 纯指数退避 |
|------|-------|-----------|
| 实时性 | 毫秒级 | 秒级 |
| API Server 压力 | 低（长连接） | 中（轮询） |
| 实现复杂度 | 中 | 低 |
| 依赖 | informer | 无 |

决策：watch 优先 + 指数退避降级，兼顾实时性与健壮性。

### 8.3 为什么 env 脚本并发度限制为 4

- 节点 IO 带宽有限，过高并发会导致下载失败
- 4 是经验值，可在后续压测中调整
- 通过 `errgroup.SetLimit` 可灵活配置

## 9. 附录

### 9.1 改造文件清单

| 文件 | 方案 | 改造类型 |
|------|------|----------|
| `pkg/kube/clientcache.go` | 1 | 新增 |
| `pkg/kube/kube.go` | 1 | 修改 |
| `cmd/manager/main.go` | 1 | 修改 |
| `pkg/mergecluster/bkecluster.go` | 2、4 | 修改 |
| `pkg/command/command.go` | 3 | 修改 |
| `pkg/phaseframe/phases/ensure_master_join.go` | 5 | 修改 |
| `pkg/phaseframe/phases/ensure_nodes_env.go` | 6 | 修改 |
| `pkg/phaseframe/phasecontext.go` | 7 | 修改 |
| 各 phase 的 `NeedExecute`/`Execute` | 7 | 修改 |

### 9.2 不改造的部分（已优化）

| 设计点 | 位置 | 状态 |
|--------|------|------|
| DAG 批次内并发 | `pkg/dagexec/scheduler.go:124-180` | 已使用 `errgroup` + 信号量限流，无需改造 |
| SSH 推送并发 | `pkg/phaseframe/phaseutil/agentssh/push_upgrade.go:54-90` | 已使用 `bkessh.MultiCli` 并发推送，无需改造 |
| Worker join 状态检查 | `pkg/phaseframe/phases/ensure_worker_join.go:483-493` | 已使用 `sync.WaitGroup` 并发，无需改造 |

### 9.3 术语表

| 术语 | 含义 |
|------|------|
| ClientCache | 远程集群客户端缓存层 |
| ClusterSnapshot | 单次 patch 流程内的集群快照 |
| Command Watch | 通过 informer 监听 Command CR 状态变更 |
| SyncStatus Backoff | 状态同步的指数退避重试 |
| PhaseContext NodeCache | reconcile 周期内的节点列表缓存 |

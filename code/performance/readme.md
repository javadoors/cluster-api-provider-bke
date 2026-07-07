# 工程中存在的关键性能瓶颈设计点（按影响面排序）：

## 一、轮询机制相关瓶颈

### 1. `SyncStatusUntilComplete` 串行重试 + 随机 sleep

[mergecluster/bkecluster.go:48-83](file:///cluster-api-provider-bke/pkg/mergecluster/bkecluster.go#L48-L83)
```go
ctx, cancel := context.WithTimeout(context.Background(), SyncStatusTimeout) // 2min
for {
    err = UpdateCombinedBKECluster(ctx, c, bkeCluster, []string{}, patchs...)
    if apierrors.IsConflict(err) { continue }      // 冲突立即重试，无 backoff
    time.Sleep(time.Duration(rand.IntnRange(0, MaxSleepSeconds)) * time.Second) // 0-2s 随机睡
    break
}
```
问题：
- 每次循环都调用 `UpdateCombinedBKECluster`，而该函数内部又包含 3 次 API Server 调用（见下文）。
- 冲突时 `continue` 无指数退避，高并发更新时易形成"冲突风暴"。
- 即使成功也会强制 `time.Sleep(0~2s)`，单次状态同步最少消耗一次随机延迟。

### 2. `UpdateCombinedBKECluster` 单次调用触发多次 GET
[mergecluster/bkecluster.go:326-389](file:///cluster-api-provider-bke/pkg/mergecluster/bkecluster.go#L326-L389)

一次更新流程内顺序发起：
1. `prepareClusterData` → `GetCombinedBKECluster` → BKECluster GET + ConfigMap GET
2. `initializePatchHelper` → 再次 BKECluster GET（重建 patch helper）
3. `processNodeData` 内部 `getBkeClusterAssociateNodesCM` → 再次 ConfigMap GET

即同一个 BKECluster 在一次 patch 中可能被 GET 3 次以上，叠加 `SyncStatusUntilComplete` 的循环放大后 API Server 压力显著。

### 3. 命令完成等待的固定 2s 轮询
[command/command.go:378-396](file:///cluster-api-provider-bke/pkg/command/command.go#L378-L396)
```go
err := wait.PollImmediateUntil(b.WaitInterval, func() (bool, error) {
    command, err := b.GetCommand()  // 每轮一次 GET
    ...
}, ctxTimeout.Done())
```
`DefaultWaitInterval = 2 * time.Second`，所有 ENV/Ping/Upgrade/Reset/Collect 命令均走此路径。在大规模节点场景下，每个命令每 2 秒拉取一次 CR，命令下发到完成的窗口期会产生大量空轮询；无指数退避，也无 watch 机制。

### 4. master join 的 1s 轮询 + 每节点一次 GET Machine
[phases/ensure_master_join.go:303-329](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_join.go#L303-L329)
```go
err := wait.PollImmediateUntil(1*time.Second, func() (bool, error) {
    for i, node := range params.NodesToJoin {
        machine, err := phaseutil.NodeToMachine(...) // 每节点一次 GET/List
        ...
    }
}, params.Timeout.Done())
```
轮询周期 1s，且每轮内对每个待加入节点单独查 Machine。N 个 master × M 次轮询 = N×M 次 Machine 查询；缺少 list 一次性拉取 + watch。

## 二、客户端创建无缓存

[kube/kube.go:158-180](file:////cluster-api-provider-bke/pkg/kube/kube.go#L158-L180) 和 [kube/kube.go:232-248](file:////cluster-api-provider-bke/pkg/kube/kube.go#L232-L248)
```go
func NewRemoteClientByBKECluster(...) (RemoteKubeClient, error) {
    config, err := remote.RESTConfig(ctx, "cluster-cache-tracker", c, util.ObjectKey(bkeCluster))
    ...
    return NewClientFromRestConfig(ctx, config)  // 每次新建 ClientSet + DynamicClient
}

func GetTargetClusterClient(...) {
    remoteClient, err := NewRemoteClientByCluster(ctx, c, cluster)
    cs, dc := remoteClient.KubeClient()
    return cs, dc, nil
}
```
问题：
- 每次调用都重建 `kubernetes.Clientset` 和 `dynamic.Interface`，重建底层 HTTP/连接池，TLS 握手成本高。
- 代码内没有 `sync.Map`/`cache`/`Once` 等缓存手段（grep 结果证实仅 `addToScheme sync.Once` 用于 scheme 注册）。
- 这些函数被各 phase、addon 安装、health check、collect 反复调用，相同 BKECluster 短时间内被多次重建 client。
- 对比 CAPI 的 `remote.ClusterClientTracker` 本身带缓存，但本工程封装层把它又"无缓存化"了。

## 三、阶段执行串行化

### 5. 脚本/命令的串行下发
[phases/ensure_nodes_env.go:445-489](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L445-L489)
```go
for _, script := range commonEnvExtraExecScripts { ... }   // 串行
for _, script := range otherCustomScripts { ... }          // 串行
```
`install-lxcfs.sh / install-nfsutils.sh / install-etcdctl.sh / install-helm.sh / install-calicoctl.sh / update-runc.sh / clean-docker-images.py` 等是相互独立的脚本，却顺序 `Wait()` 完成。每个 `Wait()` 又是 2s 轮询，串行 + 轮询叠加，节点环境初始化耗时被显著拉长。

### 6. master init / master join 串行
[phases/ensure_master_init.go](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_init.go) 与 [phases/ensure_master_join.go](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_master_join.go) 整体走单 Command + 单次 `Wait()`，依赖 KCP 的副本扩缩容间接串行加入。
对比 [phases/ensure_worker_join.go:483-493](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_worker_join.go#L483-L493) worker 节点状态检查已经用 `sync.WaitGroup` 并发：
```go
wg := sync.WaitGroup{}
for i, node := range e.nodesToJoin {
    wg.Add(1)
    go func(index int, n confv1beta1.Node) { ... }(i, node)
}
wg.Wait()
```
worker 侧已优化，master 侧未对齐，多 master 扩容时存在不必要的串行等待。

## 四、`NeedExecute` 重复拉取节点

[phases/ensure_nodes_env.go:64-75](file:///cluster-api-provider-bke/pkg/phaseframe/phases/ensure_nodes_env.go#L64-L75)
```go
func (e *EnsureNodesEnv) NeedExecute(...) bool {
    bkeNodes, err := nodeFetcher.GetBKENodesWrapperForCluster(e.Ctx, new)
    ...
}
func (e *EnsureNodesEnv) getNodesToInitEnv() bkenode.Nodes {
    bkeNodes, err := nodeFetcher.GetBKENodesWrapperForCluster(e.Ctx, bkeCluster) // 又拉一次
    ...
}
```
`NeedExecute` 与 `Execute` 各自调用一次 `GetBKENodesWrapperForCluster`（通常 List BKENodes），同一 reconcile 周期内节点数据被 List 两次。各 phase 重复此模式，叠加后 List BKENode 调用次数翻倍。

## 五、值得肯定的设计（对比参考）

为了体现"瓶颈点"的相对性，下面这些设计已经规避了同类问题：
- [dagexec/scheduler.go:124-180](file:///cluster-api-provider-bke/pkg/dagexec/scheduler.go#L124-L180)：DAG 批次内 `errgroup` + 信号量限流（`defaultMaxParallelPerBatch=8`），并发度可控。
- [phaseutil/agentssh/push_upgrade.go:54-90](file:///cluster-api-provider-bke/pkg/phaseframe/phaseutil/agentssh/push_upgrade.go#L54-L90)：SSH 升级使用 `bkessh.MultiCli` 并发推送到多节点。
- worker join 状态检查用 `sync.Map` + `WaitGroup` 并发。

## 六、优先级建议

| 优先级 | 瓶颈点 | 优化方向 |
|--------|--------|----------|
| 高 | 客户端无缓存 (#2) | 引入按 cluster key 的 `client.Cache`，复用 ClientSet |
| 高 | `UpdateCombinedBKECluster` 多次 GET (#1.2) | 单次 reconcile 内合并 GET，使用 patch helper 的原始对象 |
| 高 | 命令等待 2s 固定轮询 (#1.3) | 改 informer/watch 或指数退避，至少 5s 起步 |
| 中 | `SyncStatusUntilComplete` 无 backoff (#1.1) | 冲突重试改为指数退避（`wait.Backoff`） |
| 中 | master join 1s 轮询 + 单点查询 (#1.4) | 改 list + watch Machine，或复用 controller cache |
| 中 | env 脚本串行 (#3.5) | 独立脚本并发下发，依赖脚本再串行 |
| 低 | `NeedExecute`/`Execute` 重复 List (#4) | 在 PhaseContext 中缓存节点列表 |
       
            
#  6 个瓶颈点的完整改造方案

按优先级排列，每个方案包含设计思路、关键代码骨架和落地点。

## 方案 1：远程集群客户端缓存（高优先级）

**目标**：消除 `NewRemoteClientByBKECluster` / `GetTargetClusterClient` 的重复 clientset 构建。

**设计**：在 `pkg/kube` 中引入 `ClientCache`，按 `cluster key` 缓存 `RemoteKubeClient`，使用 `sync.Map` + TTL 过期。

### 新增 `pkg/kube/clientcache.go`

```go
package kube

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

const (
	defaultClientTTL = 10 * time.Minute
)

// ClientCache 缓存按 BKECluster 维度的 RemoteKubeClient，避免重复构建 ClientSet/DynamicClient
type ClientCache struct {
	localClient client.Client
	ttl         time.Duration

	mu    sync.RWMutex
	items map[types.NamespacedName]*cacheEntry

	sf singleflight.Group // 防止同 key 并发重建
}

type cacheEntry struct {
	remote    RemoteKubeClient
	expiresAt time.Time
}

func NewClientCache(localClient client.Client, ttl time.Duration) *ClientCache {
	if ttl == 0 {
		ttl = defaultClientTTL
	}
	return &ClientCache{
		localClient: localClient,
		ttl:         ttl,
		items:       make(map[types.NamespacedName]*cacheEntry),
	}
}

// GetClientFromCache 命中即返回；未命中则通过 builder 构建并缓存
func (c *ClientCache) GetClientFromCache(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) (RemoteKubeClient, error) {
	key := types.NamespacedName{Namespace: bkeCluster.Namespace, Name: bkeCluster.Name}

	// fast path
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

// Invalidate 失效某个 cluster 的缓存（用于 cluster 重建/token 轮换后）
func (c *ClientCache) Invalidate(namespace, name string) {
	key := types.NamespacedName{Namespace: namespace, Name: name}
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}
```

### 改造 `pkg/kube/kube.go`

```go
// 全局单例（在 controller 初始化时注入）
var globalClientCache *ClientCache

func InitClientCache(localClient client.Client) {
	globalClientCache = NewClientCache(localClient, defaultClientTTL)
}

// GetTargetClusterClient 改为优先走缓存
func GetTargetClusterClient(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster) (*kubernetes.Clientset, dynamic.Interface, error) {
	if globalClientCache != nil {
		remote, err := globalClientCache.GetClientFromCache(ctx, bkeCluster)
		if err == nil {
			cs, dc := remote.KubeClient()
			return cs, dc, nil
		}
		// 缓存失败回退到原路径
	}
	// 原有逻辑保持不变作为兜底
	cluster, err := util.GetOwnerCluster(ctx, c, bkeCluster.ObjectMeta)
	...
}
```

### 落地点
- `cmd/manager/main.go` 启动时调用 `kube.InitClientCache(mgr.GetClient())`
- `BKECluster` controller 的 reconciler 在 token 轮换或 cluster 删除时调用 `Invalidate`
- 失败回退保证兼容性

## 方案 2：合并 `UpdateCombinedBKECluster` 内的多次 GET（高优先级）

**目标**：将单次 patch 流程内的 3 次 GET（BKECluster + ConfigMap + BKECluster）合并为 1 次。

**设计**：在 `mergecluster` 包内引入 `ClusterSnapshot`，一次 GET 后在流程内复用。

### 改造 `pkg/mergecluster/bkecluster.go`

```go
// ClusterSnapshot 一次 reconcile 内的集群快照，避免重复 GET
type ClusterSnapshot struct {
	BKECluster  *v1beta1.BKECluster
	ConfigMap   *corev1.ConfigMap
	patchHelper *patch.Helper
}

// LoadClusterSnapshot 一次性加载 BKECluster + ConfigMap，构建 patch helper
func LoadClusterSnapshot(ctx context.Context, c client.Client, key types.NamespacedName) (*ClusterSnapshot, error) {
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

	// 复用 snap.BKECluster 作为 currentBkeCluster
	currentBkeCluster := snap.BKECluster
	patchHelper := snap.patchHelper

	// 复用 snap.ConfigMap，不再调用 getBkeClusterAssociateNodesCM 二次 GET
	cm := snap.ConfigMap
	nodesCM := &bkeNodes{}
	if v, ok := cm.Data["nodes"]; ok {
		_ = json.Unmarshal([]byte(v), &nodesCM.spec)
	}

	// ... 后续 processNodeData / updateClusterAndConfigMap 改为接收 snap 内对象
}
```

### 落地点
- `updateCombinedBKEClusterWithParams` 删除内部对 `prepareClusterData` / `initializePatchHelper` / `getBkeClusterAssociateNodesCM` 的独立调用
- API 调用从 3 次降为 1 次 GET（BKECluster）+ 1 次 GET（ConfigMap，本身无法避免）

## 方案 3：命令完成等待改为 watch + 指数退避（高优先级）

**目标**：消除 `waitCommandComplete` 的 2s 固定轮询。

**设计**：优先使用 informer watch；不可用时降级为指数退避轮询。

### 改造 `pkg/command/command.go`

```go
// waitForCommandCompletion 改造为 watch-first
func (b *BaseCommand) waitForCommandCompletion(complete *bool, successNodes, failedNodes *[]string) error {
	ctxTimeout, cancel := context.WithTimeout(b.Ctx, b.WaitTimeout)
	defer cancel()

	// 优先尝试 watch（要求 b.Client 是 controller-runtime client，已带 cache）
	if err := b.watchCommandComplete(ctxTimeout, complete, successNodes, failedNodes); err == nil {
		return nil
	}
	// 降级到指数退避轮询
	return b.pollCommandCompleteWithBackoff(ctxTimeout, complete, successNodes, failedNodes)
}

func (b *BaseCommand) watchCommandComplete(ctx context.Context, complete *bool, successNodes, failedNodes *[]string) error {
	// 使用 client.Watch 监听单条 Command
	watcher, err := b.Client.Watch(ctx, &agentv1beta1.CommandList{}, client.InNamespace(b.commandNS), client.MatchingFields{"metadata.name": b.commandName})
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

// pollCommandCompleteWithBackoff 指数退避兜底
func (b *BaseCommand) pollCommandCompleteWithBackoff(ctx context.Context, complete *bool, successNodes, failedNodes *[]string) error {
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

### 落地点
- `pkg/command/command.go:378` 替换原 `wait.PollImmediateUntil`
- 改造前提：controller 已为 `agentv1beta1.Command` 注册 informer，否则降级路径生效

## 方案 4：`SyncStatusUntilComplete` 引入指数退避（中优先级）

**目标**：消除冲突重试风暴和强制 sleep。

### 改造 `pkg/mergecluster/bkecluster.go`

```go
const (
	SyncStatusTimeout = 5 * time.Minute // 适当延长，配合 backoff
)

func SyncStatusUntilComplete(c client.Client, bkeCluster *v1beta1.BKECluster, patchs ...PatchFunc) (err error) {
	log := log.With("name", "syncer")
	ctx, cancel := context.WithTimeout(context.Background(), SyncStatusTimeout)
	defer cancel()

	// 使用指数退避 + jitter
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
**收益**：
- 删除强制 `time.Sleep(0~2s)`（成功路径立即返回）
- 冲突从 `continue` 改为指数退避（100ms → 5s）

## 方案 5：master join 轮询改为 List + 指数退避（中优先级）

**目标**：将 N 次单点 `NodeToMachine` 改为 1 次 List + 退避。

### 改造 `pkg/phaseframe/phases/ensure_master_join.go`

```go
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
			// 通过 annotation 或 label 关联 IP（假设 Machine 有 IP label）
			ip, ok := m.Labels[bkev1beta1.NodeIPLabel]
			if !ok {
				continue
			}
			if idx, ok := pending[ip]; ok {
				params.Log.Info(constant.MasterJoinSucceedReason, "Master node join success. node: %v", phaseutil.NodeInfo(params.NodesToJoin[idx]))
				params.SuccessJoinNode[idx] = params.NodesToJoin[idx]
				delete(pending, ip)
			}
		}

		if len(pending) == 0 {
			params.Log.Info(constant.MasterJoiningReason, "All master joined, total: %d", len(params.NodesToJoin))
			return true, nil
		}
		if pollCount%LogOutputInterval == 0 {
			params.Log.Info(constant.MasterJoiningReason, "Wait master join. success: %d, pending: %d", len(params.SuccessJoinNode), len(pending))
		}
		return false, nil
	})
	return err
}
```
**收益**：N 次 GET Machine → 1 次 List Machine；2s 固定 → 2~10s 指数退避。

## 方案 6：env 脚本并发化（中优先级）

**目标**：将相互独立的 `install-*.sh` 脚本并发下发。

### 改造 `pkg/phaseframe/phases/ensure_nodes_env.go`

```go
// 脚本依赖图：file-downloader.sh / package-downloader.sh 是其他脚本的前置
var scriptDependencies = map[string][]string{
	"install-lxcfs.sh":        {"file-downloader.sh", "package-downloader.sh"},
	"install-nfsutils.sh":     {"file-downloader.sh", "package-downloader.sh"},
	"install-etcdctl.sh":      {"file-downloader.sh", "package-downloader.sh"},
	"install-helm.sh":         {"file-downloader.sh", "package-downloader.sh"},
	"install-calicoctl.sh":    {"file-downloader.sh", "package-downloader.sh"},
	"update-runc.sh":          {"file-downloader.sh", "package-downloader.sh"},
	"clean-docker-images.py":  {"file-downloader.sh", "package-downloader.sh"},
}

// executeExtraExecScriptsConcurrent 并发执行独立脚本，前置依赖串行
func (e *EnsureNodesEnv) executeExtraExecScriptsConcurrent(scripts []string) error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()

	// 阶段 1：串行执行 common 脚本（file-downloader / package-downloader 是后续依赖）
	common := []string{"file-downloader.sh", "package-downloader.sh"}
	for _, script := range common {
		if err := e.executeSingleScript(ctx, c, bkeCluster, scheme, log, script); err != nil {
			return err
		}
	}

	// 阶段 2：并发执行其他独立脚本
	rest := make([]string, 0, len(scripts))
	for _, s := range scripts {
		if !utils.ContainsString(common, s) {
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

func (e *EnsureNodesEnv) executeSingleScript(ctx context.Context, c client.Client, cluster *bkev1beta1.BKECluster, scheme *runtime.Scheme, log *bkev1beta1.BKELogger, script string) error {
	// 抽取原 installCommonScripts/installOtherCustomScripts 中单脚本逻辑
	// ...
	return nil
}
```
**收益**：7 个独立脚本从 7×（下发+2s 轮询）变为 1×前置 + max(4)×并发。

## 方案 7：PhaseContext 内缓存 NodeFetcher 结果（低优先级）

**目标**：消除 `NeedExecute` + `Execute` 在同一 reconcile 内对 `GetBKENodesWrapperForCluster` 的重复 List。

### 改造 `pkg/phaseframe/phasecontext.go`（或现有 PhaseContext 定义）

```go
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

### 改造各 phase

```go
// ensure_nodes_env.go
func (e *EnsureNodesEnv) NeedExecute(...) bool {
	bkeNodes, err := e.Ctx.GetCachedBKENodes(new) // 改为缓存版本
	...
}

func (e *EnsureNodesEnv) getNodesToInitEnv() bkenode.Nodes {
	bkeNodes, err := e.Ctx.GetCachedBKENodes(bkeCluster) // 复用缓存
	...
}
```

### 落地点
- 凡是修改 BKENode 状态的 phase（如 `SetNodeStateWithMessageForCluster`）执行后调用 `InvalidateNodeCache()`
- reconcile 入口处重置 `nodeCacheOnce`

## 落地路线图

| 阶段 | 方案 | 风险 | 验证方式 |
|------|------|------|----------|
| P0 | 方案 1（client 缓存） | 低，有降级 | 单测 + 集成测试验证 client 复用 |
| P0 | 方案 2（合并 GET） | 中，影响状态写回 | 重点回归 `SyncStatusUntilComplete` 路径 |
| P0 | 方案 3（watch 等待） | 中，依赖 informer | 验证降级路径在无 cache 时正常 |
| P1 | 方案 4（指数退避） | 低 | 单测验证 backoff 行为 |
| P1 | 方案 5（List 替代 GET） | 中，依赖 Machine label | 验证 Machine 关联 IP 的方式 |
| P2 | 方案 6（脚本并发） | 中，需梳理依赖 | 集成测试验证脚本执行顺序 |
| P2 | 方案 7（节点缓存） | 低 | 单测验证缓存失效逻辑 |

## 验证指标

改造完成后建议监控以下指标确认收益：
- API Server QPS（应下降 30~50%）
- `BKECluster` reconcile P99 时延（master join / env init 应下降明显）
- `Command` CR 的 GET 次数 / 命令数（应接近 1）
- etcd watch 事件数（方案 3 上线后应上升）

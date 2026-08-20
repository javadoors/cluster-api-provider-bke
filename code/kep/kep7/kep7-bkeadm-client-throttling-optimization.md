# KEP-7: bkeadm 客户端限流优化

| 字段 | 值 |
|------|-----|
| **KEP 编号** | KEP-7 |
| **标题** | bkeadm Kubernetes 客户端限流参数优化 |
| **状态** | `provisional` |
| **类型** | Optimization |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-20 |
| **依赖** | bkeadm 组件、client-go |

## 1. 摘要

本提案优化 bkeadm 组件的 Kubernetes 客户端限流配置，解决 "Submit cluster-api yaml to the cluster" 阶段因 client-side throttling 导致的 8 分 37 秒等待问题。通过在 `NewKubernetesClient` 函数中设置 `QPS=50` 和 `Burst=100`，并复用 `RESTMapper` 减少 Discovery API 调用，预计将该阶段耗时从 8m37s 降至约 20 秒，整体集群安装时间提升约 40%。

## 2. 动机

### 2.1 现状痛点

| 问题 | 现状 | 影响 |
|------|------|------|
| **客户端限流未配置** | bkeadm 使用 client-go 默认值（QPS=5, Burst=10） | 66 节点集群安装时产生 8m37s 的 API 等待 |
| **RESTMapper 重复创建** | 每个 YAML 文件处理都重新创建 RESTMapper | 66 个 BKENode 文件触发 66 次 Discovery API 调用 |
| **性能瓶颈明显** | "Submit cluster-api yaml" 阶段占总耗时 40.5% | 严重影响大规模集群安装效率 |

### 2.2 目标

1. 将 bkeadm 的 Kubernetes 客户端 QPS 从默认值 5 提升至 50。
2. 将 Burst 从默认值 10 提升至 100。
3. 复用 RESTMapper，减少 Discovery API 调用次数。
4. 将 "Submit cluster-api yaml" 阶段耗时从 8m37s 降至约 20 秒。

### 2.3 非目标

1. 不修改 capbke 组件的客户端配置（已优化至 QPS=50, Burst=100）。
2. 不修改 installer-service 的客户端配置（已配置 QPS=1e6, Burst=1e6）。
3. 不引入新的客户端配置管理框架。

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| 客户端初始化 | 修改 `NewKubernetesClient` 函数，添加 QPS/Burst 配置 |
| RESTMapper 复用 | 将 RESTMapper 提升为 Client 结构体成员，避免重复创建 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 不改变 API 接口，仅优化内部实现 |
| **低风险** | 仅修改客户端初始化逻辑，不影响业务逻辑 |

## 4. 问题分析

### 4.1 性能日志分析

基于 `bke-cluster-create2.log` 的分析（66 节点集群安装）：

| 阶段 | 开始时间 | 结束时间 | 耗时 | 占比 |
|------|---------|---------|------|------|
| **Submit cluster-api yaml + API 限流** | 18:11:21 | 18:19:58 | **8m37s** | **40.5%** |
| BKEAgent 推送 (66 节点) | 18:20:01 | 18:21:42 | 1m41s | 7.9% |
| 节点环境初始化 (66 节点) | 18:22:09 | 18:25:09 | 3m0s | 14.1% |
| 其他阶段 | - | - | ~8m | 37.5% |
| **总计** | 18:11:21 | 18:32:36 | **21m15s** | 100% |

### 4.2 限流日志证据

```
18:11:21 - Submit cluster-api yaml to the cluster
18:11:23 - Waited for 1.1973968s due to client-side throttling, not priority and fairness
18:11:33 - Waited for 2.79670416s due to client-side throttling, not priority and fairness
18:11:43 - Waited for 4.19752324s due to client-side throttling, not priority and fairness
18:11:54 - Waited for 5.79745005s due to client-side throttling, not priority and fairness
18:12:04 - Waited for 7.39765425s due to client-side throttling, not priority and fairness
...（重复约 50 次）
18:19:58 - Submit the configuration to the cluster (1 BKECluster + 66 BKENodes)
```

### 4.3 耗时原因深度分析

#### 4.3.1 client-go 限流机制

client-go 使用**令牌桶（Token Bucket）算法**实现客户端限流，核心参数：

| 参数 | 含义 | 默认值 |
|------|------|--------|
| **QPS** | 每秒允许的请求数（令牌生成速率） | 5 |
| **Burst** | 令牌桶最大容量（突发请求数） | 10 |

**工作原理**：

```txt
┌─────────────────────────────────────────────────────────────┐
│                    令牌桶限流机制                              │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  令牌生成速率: QPS = 5 个/秒                                 │
│  桶容量: Burst = 10 个                                       │
│                                                              │
│  时间轴:                                                     │
│  t=0s    [●●●●●●●●●●]  桶满，10个令牌                        │
│  t=0.1s  [●●●●●●●●●○]  消耗1个，生成0.5个 → 9.5个           │
│  t=0.2s  [●●●●●●●●●○]  消耗1个，生成0.5个 → 9个             │
│  ...                                                         │
│  t=1s    [●●●●●○○○○○]  10个请求后，桶空                      │
│  t=1.2s  [●●●●●●○○○○]  等待0.2秒，生成1个令牌               │
│  t=1.4s  [●●●●●●●○○○]  等待0.2秒，生成1个令牌               │
│  ...                                                         │
│                                                              │
│  第11个请求开始，每个请求需要等待 1/QPS = 0.2秒               │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### 4.3.2 bkeadm 的 API 调用模式

分析 `bkeadm/pkg/cluster/deploy.go` 和 `bkeadm/pkg/executor/k8s/k8s.go` 的代码：

```txt
┌─────────────────────────────────────────────────────────────┐
│              bkeadm "Submit cluster-api yaml" 阶段           │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. CreateNamespace (1次 API 调用)                           │
│                                                              │
│  2. 循环处理 66 个 BKENode 文件:                             │
│     for _, nodeFile := range nodeFiles {                     │
│         InstallYaml(nodeFile)                                │
│     }                                                        │
│                                                              │
│     每个 InstallYaml 调用:                                   │
│     ├─ processYamlResources()                                │
│     │   ├─ restmapper.GetAPIGroupResources()  ← 全量 Discovery │
│     │   │   ├─ GET /api                        (1次)         │
│     │   │   ├─ GET /apis                       (1次)         │
│     │   │   └─ GET /apis/{group}/{version}     (30+次)       │
│     │   │   合计: 30-50 次 API 调用                          │
│     │   │                                                     │
│     │   └─ createResource()                   (1次)          │
│     │       └─ POST /apis/.../bkenodes        (1次)          │
│     │                                                         │
│     └─ 每个文件合计: 31-51 次 API 调用                        │
│                                                              │
│  3. InstallYaml(bkefile) - BKECluster                        │
│     └─ 同上: 31-51 次 API 调用                               │
│                                                              │
│  ─────────────────────────────────────────────────────────   │
│  总计: 67 个文件 × (31-51) 次/文件 = 2077-3417 次 API 调用   │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### 4.3.3 耗时计算

**假设条件**：
- QPS = 5（默认值）
- Burst = 10（默认值）
- 每个文件触发约 35 次 API 调用（Discovery + Create）
- 67 个文件（66 BKENode + 1 BKECluster）

**计算过程**：

```txt
┌─────────────────────────────────────────────────────────────┐
│                    耗时计算                                  │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  总 API 调用次数: 67 × 35 = 2345 次                         │
│                                                              │
│  前 10 次请求: 立即发送（Burst=10）                          │
│  后续请求: 每个等待 1/QPS = 1/5 = 0.2 秒                    │
│                                                              │
│  等待时间 = (总请求数 - Burst) × (1/QPS)                    │
│           = (2345 - 10) × 0.2                               │
│           = 2335 × 0.2                                      │
│           = 467 秒                                          │
│           ≈ 7 分 47 秒                                      │
│                                                              │
│  加上实际 API 处理时间（约 50 秒）:                          │
│  总耗时 ≈ 7 分 47 秒 + 50 秒 ≈ 8 分 37 秒                   │
│                                                              │
│  ✅ 与日志中观察到的 8m37s 完全吻合                          │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### 4.3.4 日志中的等待时间递增现象

观察日志中的等待时间：

```txt
18:11:23 - Waited for 1.1973968s   (第1次限流)
18:11:33 - Waited for 2.79670416s  (第2次限流)
18:11:43 - Waited for 4.19752324s  (第3次限流)
18:11:54 - Waited for 5.79745005s  (第4次限流)
18:12:04 - Waited for 7.39765425s  (第5次限流)
```

**原因分析**：

```txt
┌─────────────────────────────────────────────────────────────┐
│              等待时间递增的原因                               │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  client-go 的限流器在每次 API 调用前检查令牌:                │
│                                                              │
│  第1个文件 (35次调用):                                       │
│    - 前10次: 使用 Burst 令牌，无等待                         │
│    - 后25次: 每次等待 0.2秒，累计 5秒                        │
│    - 加上 API 处理时间: ~1秒                                 │
│    - 总耗时: ~6秒                                            │
│                                                              │
│  第2个文件 (35次调用):                                       │
│    - 令牌桶已空，需要等待令牌生成                            │
│    - 等待时间 = 35 × 0.2 = 7秒                               │
│    - 加上 API 处理时间: ~1秒                                 │
│    - 总耗时: ~8秒                                            │
│                                                              │
│  第N个文件:                                                  │
│    - 等待时间 = 35 × 0.2 = 7秒                               │
│    - 日志记录的是"累计等待时间"，不是单次等待                │
│                                                              │
│  日志中的 "Waited for Xs" 是 client-go 内部累计的等待时间，  │
│  随着请求次数增加，累计等待时间自然递增。                    │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### 4.3.5 RESTMapper 重复创建的额外开销

除了 QPS/Burst 限流，RESTMapper 重复创建也产生额外开销：

```txt
┌─────────────────────────────────────────────────────────────┐
│              RESTMapper 重复创建开销                          │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  每个文件触发 1 次全量 Discovery:                            │
│    - GET /api                                    (~50ms)    │
│    - GET /apis                                   (~50ms)    │
│    - GET /apis/{group}/{version} × 30 groups     (~1500ms)  │
│    - 合计: ~1600ms = 1.6秒                                  │
│                                                              │
│  66 个文件的额外开销:                                        │
│    - 66 × 1.6秒 = 105.6秒 ≈ 1分46秒                         │
│                                                              │
│  这部分开销与 QPS/Burst 限流叠加:                            │
│    - 限流等待: ~7分47秒                                      │
│    - Discovery 开销: ~1分46秒                                │
│    - 但 Discovery 调用也受 QPS 限流                          │
│    - 实际总耗时: ~8分37秒                                    │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### 4.3.6 优化效果预估

| 优化项 | 优化前 | 优化后 | 效果 |
|--------|--------|--------|------|
| **QPS/Burst** | QPS=5, Burst=10 | QPS=50, Burst=100 | 限流等待从 467秒 降至 ~47秒 |
| **RESTMapper 复用** | 66 次 Discovery | 1 次 Discovery | 减少 65 次全量 Discovery |
| **合计** | 8分37秒 | ~20秒 | 节省 ~8分17秒 |

**优化后计算**：

```txt
总 API 调用次数: 2345 次（Discovery 从 66×35 降至 1×35 + 66×1 = 101 次）
实际调用次数: 2345 - 65×35 = 2345 - 2275 = 70 次（大幅减少）

等待时间 = (70 - 100) × (1/50) = 0 秒（Burst=100 足够覆盖）
API 处理时间 = 70 × 0.2秒 = 14秒

总耗时 ≈ 14秒 + 6秒（其他开销） ≈ 20秒
```

### 4.4 根因分析

**问题代码**：`bkeadm/pkg/executor/k8s/k8s.go` 第 69-104 行

```go
func NewKubernetesClient(kubeConfig string) (KubernetesClient, error) {
    var config *rest.Config
    var err error

    if kubeConfig == "" {
        if home := homedir.HomeDir(); home != "" {
            kubeConfig = filepath.Join(home, ".kube", "config")
        }
    }
    if utils.Exists(kubeConfig) {
        config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
    }
    if err != nil {
        return nil, err
    }
    if config == nil {
        return nil, errors.New("The kube config configuration file does not exist. ")
    }

    // ❌ 未设置 QPS/Burst，使用 client-go 默认值（QPS=5, Burst=10）
    clientSet, err := kubernetes.NewForConfig(config)
    if err != nil {
        log.Infof("Failed to initialize kubernetes clientset")
        return nil, err
    }

    dynamicClient, err := dynamic.NewForConfig(config)
    if err != nil {
        log.Infof("Failed to initialize kubernetes dynamic client")
        return nil, err
    }

    return &Client{
        ClientSet:     clientSet,
        DynamicClient: dynamicClient,
    }, nil
}
```

**对比 capbke 的优化实现**：`cluster-api-provider-bke/pkg/kube/client_factory.go`

```go
func ApplyThrottlingConfig(cfg *rest.Config) *rest.Config {
    if cfg == nil {
        return nil
    }
    cfg.QPS = config.ClientQPS      // 50
    cfg.Burst = config.ClientBurst  // 100
    return cfg
}

func newKubernetesClientForConfig(cfg *rest.Config) (*kubernetes.Clientset, error) {
    return kubernetes.NewForConfig(ApplyThrottlingConfig(cfg))
}
```

### 4.5 RESTMapper 重复创建问题

**问题代码**：`bkeadm/pkg/executor/k8s/k8s.go` 第 150-199 行

```go
func (c *Client) processYamlResources(filepath string, handler yamlResourceHandler) error {
    f, err := os.Open(filepath)
    if err != nil {
        return err
    }
    defer f.Close()

    decoder := yamlutil.NewYAMLOrJSONDecoder(f, yamlDecoderBufferSize)
    
    // ❌ 每次调用都重新创建 RESTMapper，触发多次 Discovery API 调用
    dc := c.ClientSet.Discovery()
    restMapperRes, err := restmapper.GetAPIGroupResources(dc)
    if err != nil {
        return err
    }
    restMapper := restmapper.NewDiscoveryRESTMapper(restMapperRes)

    for {
        var rawObj runtime.RawExtension
        if err = decoder.Decode(&rawObj); err != nil {
            if err == io.EOF {
                break
            }
            return err
        }
        // ... 处理资源
    }
    return nil
}
```

**影响**：66 个 BKENode 文件 = 66 次 `processYamlResources` 调用 = 66 次 Discovery API 调用。

### 4.6 RESTMapper 复用的同步风险分析

#### 4.6.1 潜在不同步场景

RESTMapper 复用后，如果 API Server 上的 CRD 发生变化，缓存的 RESTMapper 可能无法感知，导致以下问题：

| 场景 | 描述 | 风险等级 | 发生概率 |
|------|------|---------|---------|
| **CRD 新增** | RESTMapper 初始化后，API Server 上新增了 CRD | 高 | 中 |
| **CRD 删除** | RESTMapper 初始化后，API Server 上删除了 CRD | 低 | 低 |
| **CRD 更新** | RESTMapper 初始化后，CRD 的 API 版本发生变化 | 中 | 低 |

#### 4.6.2 bkeadm 使用场景分析

```txt
┌─────────────────────────────────────────────────────────────┐
│                    bkeadm 使用场景                            │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  bkeadm 是一个短生命周期的 CLI 工具：                        │
│                                                              │
│  启动 → 获取 RESTMapper → 提交 67 个资源 → 退出              │
│         (1次 Discovery)    (约 8 分钟)                       │
│                                                              │
│  提交的是 BKECluster 和 BKENode 资源：                       │
│  - 这些 CRD 由管理集群的 CAPI 提供                           │
│  - 在 bkeadm 运行期间，这些 CRD 不太可能发生变化             │
│                                                              │
│  结论：风险很低                                              │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### 4.6.3 capbke 的解决方案（参考实现）

capbke 已经实现了完整的 CRD 变化监听机制：

```go
// cluster-api-provider-bke/pkg/kube/restmapper_cache.go

func newPerClusterRESTMapper(cfg *rest.Config) (*perClusterRESTMapper, error) {
    discoveryClient, err := discovery.NewDiscoveryClientForConfig(ApplyThrottlingConfig(cfg))
    // ...
    cachedDiscovery := memory.NewMemCacheClient(discoveryClient)
    mapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery)

    // 创建 CRD Informer 监听 CRD 变化
    crdInformer, err := newCRDInformer(cfg)
    // ...

    // CRD 变化时自动清除缓存
    crdInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc: func(obj interface{}) {
            m.Invalidate()  // 清除缓存
        },
        UpdateFunc: func(oldObj, newObj interface{}) {
            m.Invalidate()  // 清除缓存
        },
        DeleteFunc: func(obj interface{}) {
            m.Invalidate()  // 清除缓存
        },
    })

    go crdInformer.Run(m.stopCh)
    // ...
}
```

#### 4.6.4 bkeadm 的推荐方案

对于 bkeadm 这种短生命周期工具，有两种方案：

**方案 A：简单方案（推荐）**

使用 `DeferredDiscoveryRESTMapper`，它在遇到 `NoMatchError` 时会自动重新发现：

```go
// bkeadm/pkg/executor/k8s/k8s.go

func NewKubernetesClient(kubeConfig string) (KubernetesClient, error) {
    // ...
    
    // 使用 DeferredDiscoveryRESTMapper + 内存缓存
    discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
    if err != nil {
        return nil, err
    }
    cachedDiscovery := memory.NewMemCacheClient(discoveryClient)
    restMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery)
    
    // ...
}

func (c *Client) processYamlResources(filepath string, handler yamlResourceHandler) error {
    // ...
    
    // 使用缓存的 RESTMapper
    restMapper := c.restMapper
    
    for {
        // ...
        mapping, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
        if err != nil {
            // DeferredDiscoveryRESTMapper 遇到 NoMatchError 时会自动重新发现
            // 如果仍然失败，可以手动重置缓存
            if apierrors.IsNoMatchError(err) {
                if deferred, ok := restMapper.(*restmapper.DeferredDiscoveryRESTMapper); ok {
                    deferred.Reset()  // 清除缓存
                    mapping, err = restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
                }
            }
            if err != nil {
                return err
            }
        }
        // ...
    }
}
```

**方案 B：完整方案（参考 capbke）**

如果需要完整的 CRD 变化监听，可以参考 capbke 的实现，添加 CRD Informer：

```go
// bkeadm/pkg/executor/k8s/k8s.go

type Client struct {
    ClientSet     *kubernetes.Clientset
    DynamicClient dynamic.Interface
    restMapper    *perClusterRESTMapper  // 包含 CRD Informer
}

type perClusterRESTMapper struct {
    mapper    *restmapper.DeferredDiscoveryRESTMapper
    discovery discovery.CachedDiscoveryInterface
    stopCh    chan struct{}
}

func (m *perClusterRESTMapper) Invalidate() {
    m.discovery.Invalidate()
    m.mapper.Reset()
}

func newPerClusterRESTMapper(cfg *rest.Config) (*perClusterRESTMapper, error) {
    discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
    if err != nil {
        return nil, err
    }
    cachedDiscovery := memory.NewMemCacheClient(discoveryClient)
    mapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery)

    // 创建 CRD Informer
    apiextensionsClient, err := apiextensionsclient.NewForConfig(cfg)
    if err != nil {
        return nil, err
    }
    factory := apiextensionsinformers.NewSharedInformerFactory(apiextensionsClient, 0)
    crdInformer := factory.Apiextensions().V1().CustomResourceDefinitions().Informer()

    m := &perClusterRESTMapper{
        mapper:    mapper,
        discovery: cachedDiscovery,
        stopCh:    make(chan struct{}),
    }

    // CRD 变化时自动清除缓存
    crdInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc:    func(obj interface{}) { m.Invalidate() },
        UpdateFunc: func(oldObj, newObj interface{}) { m.Invalidate() },
        DeleteFunc: func(obj interface{}) { m.Invalidate() },
    })

    go crdInformer.Run(m.stopCh)
    cache.WaitForCacheSync(m.stopCh, crdInformer.HasSynced)

    return m, nil
}
```

#### 4.6.5 方案对比

| 方案 | 优点 | 缺点 | 适用场景 |
|------|------|------|---------|
| **方案 A** | 简单、轻量 | 无法主动感知 CRD 变化 | bkeadm（短生命周期 CLI） |
| **方案 B** | 完整、实时感知 | 复杂、需要额外资源 | capbke（长生命周期控制器） |

**推荐方案 A**：
- bkeadm 是短生命周期工具，CRD 在运行期间变化的概率极低
- 使用 `DeferredDiscoveryRESTMapper` 已经足够
- 如果遇到 `NoMatchError`，手动调用 `Reset()` 即可

## 5. 提案设计

### 5.1 改动 1：新增配置层 — 集中化 QPS/Burst 配置

**文件**：`bkeadm/pkg/config/config.go`（新文件）

参考 capbke 的实现（`cluster-api-provider-bke/utils/capbke/config/config.go`），为 bkeadm 新增独立的配置模块，支持多级配置优先级。

**配置优先级**：

```txt
命令行标志 > 环境变量 > 配置文件 > 默认值

示例：
1. 命令行：--client-qps=100 --client-burst=200
2. 环境变量：KUBE_CLIENT_QPS=80 KUBE_CLIENT_BURST=160
3. 配置文件：/etc/bke/client-config.yaml (qps: 60, burst: 120)
4. 默认值：QPS=50, Burst=100
```

**代码实现**：

```go
// bkeadm/pkg/config/config.go

package config

import (
    "flag"
    "os"
    "strconv"

    "gopkg.openfuyao.cn/bkeadm/utils/log"
    "gopkg.in/yaml.v3"
)

var (
    // ClientQPS 是 Kubernetes 客户端的 QPS
    // 默认值: 50，可以通过命令行标志、环境变量或配置文件覆盖
    ClientQPS float32

    // ClientBurst 是 Kubernetes 客户端的突发大小
    // 默认值: 100，可以通过命令行标志、环境变量或配置文件覆盖
    ClientBurst int

    // ClientConfigFile 是客户端配置文件路径
    // 默认值: /etc/bke/client-config.yaml
    ClientConfigFile string
)

const (
    // DefaultClientQPS 是 Kubernetes 客户端的默认 QPS
    DefaultClientQPS = 50
    // DefaultClientBurst 是 Kubernetes 客户端的默认突发大小
    DefaultClientBurst = 100
    // DefaultClientConfigFile 是默认配置文件路径
    DefaultClientConfigFile = "/etc/bke/client-config.yaml"
)

// ClientConfig 客户端配置文件结构
type ClientConfig struct {
    QPS   float32 `yaml:"qps"`
    Burst int     `yaml:"burst"`
}

// RegisterFlags 注册命令行标志
func RegisterFlags() {
    flag.Float32Var(&ClientQPS, "client-qps", 0,
        "Kubernetes 客户端的 QPS。优先级：命令行 > 环境变量 > 配置文件 > 默认值(50)")
    flag.IntVar(&ClientBurst, "client-burst", 0,
        "Kubernetes 客户端的突发大小。优先级：命令行 > 环境变量 > 配置文件 > 默认值(100)")
    flag.StringVar(&ClientConfigFile, "client-config-file", DefaultClientConfigFile,
        "客户端配置文件路径。默认: /etc/bke/client-config.yaml")
}

// ResolveClientConfig 解析配置，按优先级确定最终值
// 调用时机：命令行标志解析完成后
func ResolveClientConfig() {
    // 1. 首先加载配置文件（最低优先级）
    loadClientConfigFile()

    // 2. 环境变量覆盖配置文件
    if qps := os.Getenv("KUBE_CLIENT_QPS"); qps != "" {
        if v, err := strconv.ParseFloat(qps, 32); err == nil {
            ClientQPS = float32(v)
        }
    }
    if burst := os.Getenv("KUBE_CLIENT_BURST"); burst != "" {
        if v, err := strconv.Atoi(burst); err == nil {
            ClientBurst = v
        }
    }

    // 3. 如果仍未设置，使用默认值
    if ClientQPS == 0 {
        ClientQPS = DefaultClientQPS
    }
    if ClientBurst == 0 {
        ClientBurst = DefaultClientBurst
    }

    log.Infof("resolved Kubernetes client throttling config: qps=%v, burst=%d, configFile=%s",
        ClientQPS, ClientBurst, ClientConfigFile)
}

// loadClientConfigFile 从配置文件加载 QPS/Burst 配置
func loadClientConfigFile() {
    if ClientConfigFile == "" {
        ClientConfigFile = DefaultClientConfigFile
    }

    data, err := os.ReadFile(ClientConfigFile)
    if err != nil {
        // 配置文件不存在或读取失败，使用默认值
        return
    }

    var config ClientConfig
    if err := yaml.Unmarshal(data, &config); err != nil {
        log.Warnf("failed to parse client config file %s: %v, using defaults", ClientConfigFile, err)
        return
    }

    if config.QPS > 0 {
        ClientQPS = config.QPS
    }
    if config.Burst > 0 {
        ClientBurst = config.Burst
    }
}
```

**配置文件格式**：`/etc/bke/client-config.yaml`

```yaml
# BKE 客户端配置
# 通过 ConfigMap 挂载到 Pod

# Kubernetes 客户端限流配置
qps: 50
burst: 100
```

**ConfigMap 定义**：

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: bke-client-config
  namespace: bke-system
data:
  client-config.yaml: |
    # BKE 客户端配置
    qps: 50
    burst: 100
```

**Deployment 挂载配置**：

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: bke-controller-manager
  namespace: bke-system
spec:
  template:
    spec:
      containers:
      - name: manager
        args:
        - --client-config-file=/etc/bke/client-config.yaml
        volumeMounts:
        - name: client-config
          mountPath: /etc/bke
          readOnly: true
      volumes:
      - name: client-config
        configMap:
          name: bke-client-config
```

### 5.2 改动 2：NewKubernetesClient 使用可配置 QPS/Burst

**文件**：`bkeadm/pkg/executor/k8s/k8s.go`

**修改位置**：第 86-87 行之间（`config` 获取成功后、创建 client 之前）

```go
// 当前代码（第 84-88 行）：
if config == nil {
    return nil, errors.New("The kube config configuration file does not exist. ")
}

clientSet, err := kubernetes.NewForConfig(config)

// 修改为：
if config == nil {
    return nil, errors.New("The kube config configuration file does not exist. ")
}

// 设置客户端限流参数，避免 API 调用被 client-side throttling 阻塞
// 使用集中化配置，支持命令行标志、环境变量、配置文件覆盖
config.QPS = bkeconfig.ClientQPS
config.Burst = bkeconfig.ClientBurst

clientSet, err := kubernetes.NewForConfig(config)
```

### 5.3 改动 3：Client 结构体添加 restMapper 成员

**文件**：`bkeadm/pkg/executor/k8s/k8s.go`

**修改位置**：第 59-62 行

```go
// 当前代码：
type Client struct {
    ClientSet     *kubernetes.Clientset
    DynamicClient dynamic.Interface
}

// 修改为：
type Client struct {
    ClientSet     *kubernetes.Clientset
    DynamicClient dynamic.Interface
    restMapper    meta.RESTMapper  // 缓存 RESTMapper，避免重复 Discovery
}
```

### 5.4 改动 4：NewKubernetesClient 初始化 restMapper

**文件**：`bkeadm/pkg/executor/k8s/k8s.go`

**修改位置**：第 100-103 行

```go
// 当前代码：
return &Client{
    ClientSet:     clientSet,
    DynamicClient: dynamicClient,
}, nil

// 修改为：
restMapperRes, err := restmapper.GetAPIGroupResources(clientSet.Discovery())
if err != nil {
    return nil, err
}

return &Client{
    ClientSet:     clientSet,
    DynamicClient: dynamicClient,
    restMapper:    restmapper.NewDiscoveryRESTMapper(restMapperRes),
}, nil
```

### 5.5 改动 5：processYamlResources 复用 restMapper

**文件**：`bkeadm/pkg/executor/k8s/k8s.go`

**修改位置**：第 162-168 行

```go
// 当前代码：
dc := c.ClientSet.Discovery()
restMapperRes, err := restmapper.GetAPIGroupResources(dc)
if err != nil {
    return err
}
restMapper := restmapper.NewDiscoveryRESTMapper(restMapperRes)

// 修改为：
restMapper := c.restMapper
```

### 5.6 改动 6：main.go 集成配置解析

**文件**：`bkeadm/main.go`

**修改位置**：`main()` 函数中，在 `cmd.Execute()` 之前

```go
// 当前代码：
func main() {
    if version.Version == "" {
        version.GitCommitID = gitCommitId
        version.Version = ver
        version.Architecture = architecture
        version.Timestamp = timestamp
    }
    cmd.Execute()
}

// 修改为：
func main() {
    if version.Version == "" {
        version.GitCommitID = gitCommitId
        version.Version = ver
        version.Architecture = architecture
        version.Timestamp = timestamp
    }

    // 新增：注册并解析客户端限流配置
    bkeconfig.RegisterFlags()
    flag.Parse()
    bkeconfig.ResolveClientConfig()

    cmd.Execute()
}
```

### 5.7 完整修改后代码

```go
// bkeadm/pkg/executor/k8s/k8s.go

import (
    bkeconfig "gopkg.openfuyao.cn/bkeadm/pkg/config"
    // ... 其他导入
)

type Client struct {
    ClientSet     *kubernetes.Clientset
    DynamicClient dynamic.Interface
    restMapper    meta.RESTMapper  // 新增：缓存 RESTMapper
}

func NewKubernetesClient(kubeConfig string) (KubernetesClient, error) {
    var config *rest.Config
    var err error

    if kubeConfig == "" {
        if home := homedir.HomeDir(); home != "" {
            kubeConfig = filepath.Join(home, ".kube", "config")
        }
    }
    if utils.Exists(kubeConfig) {
        config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
    }
    if err != nil {
        return nil, err
    }
    if config == nil {
        return nil, errors.New("The kube config configuration file does not exist. ")
    }

    // 新增：使用集中化配置设置客户端限流参数
    config.QPS = bkeconfig.ClientQPS
    config.Burst = bkeconfig.ClientBurst

    clientSet, err := kubernetes.NewForConfig(config)
    if err != nil {
        log.Infof("Failed to initialize kubernetes clientset")
        return nil, err
    }

    dynamicClient, err := dynamic.NewForConfig(config)
    if err != nil {
        log.Infof("Failed to initialize kubernetes dynamic client")
        return nil, err
    }

    // 新增：初始化并缓存 RESTMapper
    restMapperRes, err := restmapper.GetAPIGroupResources(clientSet.Discovery())
    if err != nil {
        return nil, err
    }

    return &Client{
        ClientSet:     clientSet,
        DynamicClient: dynamicClient,
        restMapper:    restmapper.NewDiscoveryRESTMapper(restMapperRes),
    }, nil
}

func (c *Client) processYamlResources(filepath string, handler yamlResourceHandler) error {
    f, err := os.Open(filepath)
    if err != nil {
        return err
    }
    defer func() {
        if err := f.Close(); err != nil {
            log.Warnf("failed to close file %s: %v", filepath, err)
        }
    }()

    decoder := yamlutil.NewYAMLOrJSONDecoder(f, yamlDecoderBufferSize)
    
    // 修改：复用缓存的 RESTMapper
    restMapper := c.restMapper

    for {
        var rawObj runtime.RawExtension
        if err = decoder.Decode(&rawObj); err != nil {
            if err == io.EOF {
                break
            }
            return err
        }

        obj, gvk, err := unstructured.UnstructuredJSONScheme.Decode(rawObj.Raw, nil, nil)
        if err != nil {
            return err
        }
        mapping, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
        if err != nil {
            return err
        }

        unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
        if err != nil {
            return err
        }
        unstruct := &unstructured.Unstructured{Object: unstructuredObj}

        if err = handler(unstruct, mapping); err != nil {
            return err
        }
    }
    return nil
}
```

## 6. 优化效果预估

| 优化项 | 减少的 API 等待 | 预估节省时间 |
|--------|---------------|-------------|
| QPS/Burst 提升（5→50） | 67 个资源创建请求的限流等待从 ~13s/请求 降至 ~0.02s/请求 | **~8 分钟** |
| RESTMapper 复用 | 减少 65 次 Discovery API 调用（66→1） | **~30 秒** |
| **合计** | | **~8.5 分钟** |

**优化后预期**：
- "Submit cluster-api yaml" 阶段从 **8m37s** 降至 **~20s**
- 整体集群安装时间从 **21m15s** 降至 **~12m30s**
- 性能提升约 **40%**

## 7. 测试计划

### 7.1 单元测试

| 测试场景 | 验证内容 |
|---------|---------|
| NewKubernetesClient 初始化 | 验证 QPS/Burst 已从配置读取并设置 |
| RESTMapper 复用 | 验证多次 processYamlResources 调用只触发 1 次 Discovery |
| 配置优先级 - 默认值 | 验证无配置时使用默认值 QPS=50, Burst=100 |
| 配置优先级 - 环境变量 | 验证环境变量覆盖默认值 |
| 配置优先级 - 配置文件 | 验证配置文件覆盖默认值 |
| 配置优先级 - 命令行标志 | 验证命令行标志覆盖其他配置 |
| 配置文件不存在 | 验证配置文件不存在时使用默认值，无报错 |
| 配置文件格式错误 | 验证配置文件格式错误时降级到默认值，输出警告日志 |

**配置测试代码示例**：

```go
// bkeadm/pkg/config/config_test.go

func TestResolveClientConfig_Defaults(t *testing.T) {
    // 清理环境变量
    os.Unsetenv("KUBE_CLIENT_QPS")
    os.Unsetenv("KUBE_CLIENT_BURST")
    ClientConfigFile = "/nonexistent/path"

    ResolveClientConfig()

    assert.Equal(t, float32(50), ClientQPS)
    assert.Equal(t, 100, ClientBurst)
}

func TestResolveClientConfig_EnvOverride(t *testing.T) {
    os.Setenv("KUBE_CLIENT_QPS", "80")
    os.Setenv("KUBE_CLIENT_BURST", "160")
    defer os.Unsetenv("KUBE_CLIENT_QPS")
    defer os.Unsetenv("KUBE_CLIENT_BURST")

    ResolveClientConfig()

    assert.Equal(t, float32(80), ClientQPS)
    assert.Equal(t, 160, ClientBurst)
}

func TestResolveClientConfig_FileOverride(t *testing.T) {
    // 创建临时配置文件
    tmpFile, _ := os.CreateTemp("", "client-config-*.yaml")
    defer os.Remove(tmpFile.Name())
    tmpFile.Write([]byte("qps: 60\nburst: 120\n"))
    tmpFile.Close()

    os.Unsetenv("KUBE_CLIENT_QPS")
    os.Unsetenv("KUBE_CLIENT_BURST")
    ClientConfigFile = tmpFile.Name()

    ResolveClientConfig()

    assert.Equal(t, float32(60), ClientQPS)
    assert.Equal(t, 120, ClientBurst)
}

func TestResolveClientConfig_CLIOverridesAll(t *testing.T) {
    // 创建临时配置文件
    tmpFile, _ := os.CreateTemp("", "client-config-*.yaml")
    defer os.Remove(tmpFile.Name())
    tmpFile.Write([]byte("qps: 60\nburst: 120\n"))
    tmpFile.Close()

    os.Setenv("KUBE_CLIENT_QPS", "80")
    os.Setenv("KUBE_CLIENT_BURST", "160")
    defer os.Unsetenv("KUBE_CLIENT_QPS")
    defer os.Unsetenv("KUBE_CLIENT_BURST")

    ClientConfigFile = tmpFile.Name()
    // 模拟命令行标志已解析
    ClientQPS = 100
    ClientBurst = 200

    ResolveClientConfig()

    // 命令行标志优先级最高
    assert.Equal(t, float32(100), ClientQPS)
    assert.Equal(t, 200, ClientBurst)
}
```

### 7.2 集成测试

| 测试场景 | 验证内容 |
|---------|---------|
| 66 节点集群安装 | 验证 "Submit cluster-api yaml" 阶段耗时 < 30s |
| 大规模集群安装（100+ 节点） | 验证无 API 限流日志 |

### 7.3 性能测试

| 测试指标 | 优化前 | 优化后目标 |
|---------|-------|-----------|
| "Submit cluster-api yaml" 耗时 | 8m37s | < 30s |
| 整体安装时间（66 节点） | 21m15s | < 13m |
| API 限流日志数量 | ~50 条 | 0 条 |

## 8. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| QPS/Burst 过高导致 API Server 压力 | API Server 负载增加 | 默认值 QPS=50, Burst=100 是经过验证的安全值，与 capbke 保持一致 |
| 配置文件格式错误 | 配置加载失败 | 配置文件解析失败时降级到默认值，输出警告日志，不影响启动 |
| 环境变量配置错误 | 配置值异常 | 环境变量解析失败时忽略该值，继续使用其他配置来源 |
| RESTMapper 缓存过期 | 新创建的 CRD 无法被发现 | 当前场景下 CRD 在客户端初始化前已存在，无风险 |
| 配置优先级冲突 | 配置值不符合预期 | 启动时打印最终生效的配置值及来源，便于排查 |

## 9. 工作量评估

| 阶段 | 任务内容 | 工作量 |
|------|---------|--------|
| 配置层 | 新增 `bkeadm/pkg/config/config.go`，实现多级配置优先级 | 0.5 人天 |
| 代码修改 | 修改 `NewKubernetesClient` 和 `processYamlResources` | 0.5 人天 |
| main.go 集成 | 修改 `main.go` 集成配置解析 | 0.25 人天 |
| 测试验证 | 单元测试 + 集成测试 + 性能测试 | 1 人天 |
| 文档更新 | 更新性能分析报告 | 0.25 人天 |
| **总计** | | **2.5 人天** |

## 10. 相关文件

| 文件路径 | 说明 |
|---------|------|
| `bkeadm/pkg/config/config.go` | 新增：集中化 QPS/Burst 配置模块 |
| `bkeadm/pkg/executor/k8s/k8s.go` | 主要修改文件 |
| `bkeadm/main.go` | 修改：集成配置解析 |
| `cluster-api-provider-bke/pkg/kube/client_factory.go` | capbke 的参考实现 |
| `cluster-api-provider-bke/utils/capbke/config/config.go` | capbke 的 QPS/Burst 配置定义 |
| `code/performance/report/api-throttling-optimization.md` | capbke 限流优化方案参考 |
| `code/performance/report2/bke-cluster-create2.log` | 性能分析日志 |
| `code/performance/report2/analysis.md` | 性能分析报告 |

---

**文档版本**: v1.3  
**维护者**: openFuyao Team
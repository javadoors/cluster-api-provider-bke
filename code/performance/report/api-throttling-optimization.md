# KEP-10: 消除 BKE 控制器 API 限流问题

<!--
这是 Kubernetes Enhancement Proposals (KEPs) 的模板。
有关 KEP 模板的更多信息，请参见：
https://github.com/kubernetes/enhancements/blob/master/keps/NNN-template/README.md
-->

## 元数据

| 字段 | 值 |
|------|-----|
| **KEP** | 10 |
| **标题** | 消除 BKE 控制器 API 限流问题 |
| **状态** | 草案 |
| **创建日期** | 2026-07-15 |
| **最后更新** | 2026-07-15 |
| **作者** | BKE 团队 |
| **审阅者** | 待定 |
| **批准者** | 待定 |
| **SIG** | bke-performance |
| **赞助 SIG** | bke-performance |

## 摘要

本 KEP 提出消除 BKE 控制器中的 API 限流瓶颈，该瓶颈当前导致集群创建过程中出现 9 分 37 秒的延迟。限流发生的原因是 Kubernetes client-go 库使用保守的默认限流设置（QPS=5，Burst=10），这些设置无法满足 BKE 控制器的工作负载需求。

解决方案包含两个互补的优化措施：
1. **集中化 QPS/Burst 配置**：通过统一的配置管理系统，将客户端速率限制从 QPS=5/Burst=10 提高到 QPS=50/Burst=100
2. **RESTMapper 缓存**：实现带有内存缓存的全局单例 RESTMapper，消除冗余的 API 发现调用

这些更改将把 API 发现阶段从 9 分 37 秒减少到约 30 秒，提升 95%，并在整体集群创建过程中节省 11 分钟。

## 动机

### 为什么需要这个？

BKE 控制器在集群创建过程中经历严重的 API 限流，这是集群配置工作流中最大的单一瓶颈。这种限流发生在 API 资源发现阶段，此时控制器需要从管理集群的 API 服务器查询所有可用的 API 组及其版本。

### 解决什么问题？

**当前性能数据（64 节点集群）：**
- 集群创建总时间：29 分 29 秒
- API 限流持续时间：9 分 37 秒（占总时间的 32.6%）
- 限流事件：57 次
- 每个请求的平均等待时间：约 10 秒
- 最大等待时间：9.20 秒

**根因分析：**
限流是由默认的 client-go 速率限制器配置引起的：
- 默认 QPS：每秒 5 个请求
- 默认 Burst：10 个请求
- 实际工作负载：60-90 个 API 发现请求（30+ 个 API 组 × 每个 2-3 个版本）

使用这些设置，前 10 个请求立即发送（突发），但后续请求被限流，延迟呈指数增长（1s → 2s → 3s → ... → 9s），导致总等待时间接近 10 分钟。

**影响：**
- 用户体验：集群创建前 10 分钟没有可见进度
- 资源浪费：纯等待时间，没有实际的部署工作
- 可扩展性限制：随着管理集群中注册的 CRD 增多，问题会恶化

### 可衡量目标

1. 将 API 发现时间从 9 分 37 秒减少到 30 秒以内（提升 95%）
2. 消除控制器日志中的客户端限流警告
3. 将集群创建总时间从 29 分 29 秒减少到约 18 分钟（提升 39%）
4. 在增加客户端请求速率的同时保持 API 服务器稳定性

### 非目标

1. 优化 API 服务器端性能（不在本 KEP 范围内）
2. 减少管理集群中的 API 组数量
3. 实现跨多个控制器实例的分布式缓存
4. 修改 controller-runtime 框架的默认行为

## 提案

### 用户故事

**故事 1：快速集群创建**
作为集群操作员，我希望在 20 分钟内创建一个 64 节点的 Kubernetes 集群，以便能够快速响应容量需求。

*当前状态：* 集群创建需要 29 分钟以上，其中 10 分钟是纯 API 限流延迟。
*期望状态：* 集群创建在 20 分钟内完成，没有限流延迟。

**故事 2：可预测的性能**
作为集群操作员，我希望无论管理集群中注册了多少 CRD，集群创建时间都保持一致。

*当前状态：* 每个额外的 API 组都会在发现阶段增加约 10-20 秒。
*期望状态：* 无论注册的 CRD 数量如何，API 发现时间保持不变。

**故事 3：运营可见性**
作为集群操作员，我希望在集群创建过程中看到持续的进度，以便能够及早识别和解决问题。

*当前状态：* 由于 API 限流，前 10 分钟没有显示任何进度。
*期望状态：* 从集群创建开始就能看到进度。

### 注意事项/约束

1. **API 服务器负载**：将 QPS 从 5 增加到 50 将使管理集群 API 服务器的请求速率增加 10 倍。必须监控以确保 API 服务器能够处理增加的负载。

2. **向后兼容性**：更改不能破坏现有部署或要求现有用户更改配置。

3. **配置灵活性**：不同的部署场景可能需要不同的 QPS/Burst 设置。解决方案必须支持通过命令行标志和环境变量进行运行时配置。

4. **线程安全**：全局 RESTMapper 单例必须是线程安全的，以支持多个 goroutine 的并发访问。

### 实现方法

解决方案包含两个互补的优化措施：

#### 优化 A：集中化 QPS/Burst 配置

**架构：**
```
┌─────────────────────────────────────────────────────────────┐
│                      配置层                                   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  utils/capbke/config/config.go                        │   │
│  │  - ClientQPS (默认: 50)                               │   │
│  │  - ClientBurst (默认: 100)                            │   │
│  │  - 支持命令行标志和环境变量                              │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│                      工厂层                                   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/client_factory.go                           │   │
│  │  - ApplyThrottlingConfig(config)                      │   │
│  │  - NewClientFromConfig(config)                        │   │
│  │  - NewDynamicClientFromConfig(config)                 │   │
│  │  - GetManagerConfig()                                 │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│                      使用层                                   │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐       │
│  │ pkg/kube/    │  │ cmd/capbke/  │  │ cmd/bkeagent/│       │
│  │ kube.go      │  │ main.go      │  │ main.go      │       │
│  │              │  │              │  │              │       │
│  │ 使用工厂方法  │  │ 使用工厂方法  │  │ 使用工厂方法  │       │
│  └──────────────┘  └──────────────┘  └──────────────┘       │
└─────────────────────────────────────────────────────────────┘
```

**配置优先级：**
```
命令行标志 > 环境变量 > 默认值

示例：
1. 命令行：--client-qps=100 --client-burst=200
2. 环境变量：KUBE_CLIENT_QPS=80 KUBE_CLIENT_BURST=160
3. 默认值：QPS=50, Burst=100
```

#### 优化 B：带缓存的全局 RESTMapper 单例

**架构：**
```
┌─────────────────────────────────────────────────────────────┐
│                全局 RESTMapper 单例                            │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/restmapper.go                               │   │
│  │  - globalRESTMapper (单例)                            │   │
│  │  - sync.Once 用于线程安全初始化                        │   │
│  │  - memory.NewMemCacheClient 用于缓存                  │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│                      使用模式                                 │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/wait.go                                     │   │
│  │  - ToRESTMapper() 调用 GetGlobalRESTMapper()          │   │
│  │  - 第一次调用：初始化并缓存                             │   │
│  │  - 后续调用：返回缓存的实例                             │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/helm.go                                     │   │
│  │  - ToRESTMapper() 调用 GetGlobalRESTMapper()          │   │
│  │  - 与 wait.go 相同的缓存行为                          │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

**缓存失效策略：**
RESTMapper 缓存在进程生命周期内是稳定的，因为：
- BKE CRD 在控制器启动前安装（通过 Helm/kubectl）
- CRD 在控制器运行期间不会改变
- 如果将来需要动态安装/删除 CRD，可以添加缓存失效机制

## 设计细节

### API 变更

本 KEP 不引入任何新的 CRD 或 API 变更。所有更改都是控制器实现的内部更改。

### 代码变更

#### 1. 配置层

**文件：`utils/capbke/config/config.go`**

```go
var (
    // ... 现有配置 ...
    
    // ClientQPS 是 Kubernetes 客户端的 QPS
    // 默认值: 50，可以通过 --client-qps 标志或 KUBE_CLIENT_QPS 环境变量覆盖
    ClientQPS float32
    
    // ClientBurst 是 Kubernetes 客户端的突发大小
    // 默认值: 100，可以通过 --client-burst 标志或 KUBE_CLIENT_BURST 环境变量覆盖
    ClientBurst int
)

const (
    // DefaultClientQPS 是 Kubernetes 客户端的默认 QPS
    DefaultClientQPS = 50
    // DefaultClientBurst 是 Kubernetes 客户端的默认突发大小
    DefaultClientBurst = 100
)

func ConfigurationFlag() {
    // ... 现有配置 ...
    
    flag.Float32Var(&ClientQPS, "client-qps", DefaultClientQPS,
        "Kubernetes 客户端的 QPS。默认值: 50。也可以通过 KUBE_CLIENT_QPS 环境变量设置")
    flag.IntVar(&ClientBurst, "client-burst", DefaultClientBurst,
        "Kubernetes 客户端的突发大小。默认值: 100。也可以通过 KUBE_CLIENT_BURST 环境变量设置")
}

func init() {
    // 从环境变量读取
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
    
    // 如果未设置则使用默认值
    if ClientQPS == 0 {
        ClientQPS = DefaultClientQPS
    }
    if ClientBurst == 0 {
        ClientBurst = DefaultClientBurst
    }
}
```

#### 2. 工厂层

**文件：`pkg/kube/client_factory.go`（新文件）**

```go
package kube

import (
    "context"
    
    "k8s.io/client-go/dynamic"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
    ctrl "sigs.k8s.io/controller-runtime"
    
    "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/config"
)

// ApplyThrottlingConfig 将 QPS/Burst 限流配置应用到 rest.Config
// 这是客户端限流设置的唯一真实来源
func ApplyThrottlingConfig(cfg *rest.Config) *rest.Config {
    if cfg == nil {
        return cfg
    }
    
    cfg.QPS = config.ClientQPS
    cfg.Burst = config.ClientBurst
    
    return cfg
}

// NewClientFromConfig 创建应用了限流配置的新 Kubernetes 客户端
func NewClientFromConfig(cfg *rest.Config) (*kubernetes.Clientset, error) {
    cfg = ApplyThrottlingConfig(cfg)
    return kubernetes.NewForConfig(cfg)
}

// NewDynamicClientFromConfig 创建应用了限流配置的新动态客户端
func NewDynamicClientFromConfig(cfg *rest.Config) (dynamic.Interface, error) {
    cfg = ApplyThrottlingConfig(cfg)
    return dynamic.NewForConfig(cfg)
}

// GetManagerConfig 返回应用了限流配置的 controller-runtime manager 的 rest.Config
func GetManagerConfig() *rest.Config {
    return ApplyThrottlingConfig(ctrl.GetConfigOrDie())
}

// NewRemoteKubeClient 创建应用了限流配置的 RemoteKubeClient
func NewRemoteKubeClient(ctx context.Context, cfg *rest.Config) (RemoteKubeClient, error) {
    return NewClientFromRestConfig(ctx, ApplyThrottlingConfig(cfg))
}
```

#### 3. RESTMapper 单例

**文件：`pkg/kube/restmapper.go`（新文件）**

```go
package kube

import (
    "sync"
    
    "k8s.io/apimachinery/pkg/api/meta"
    "k8s.io/client-go/discovery"
    "k8s.io/client-go/discovery/cached/memory"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/restmapper"
)

var (
    globalRESTMapper     meta.RESTMapper
    globalRESTMapperOnce sync.Once
    globalRESTMapperErr  error
)

// GetGlobalRESTMapper 返回带缓存的全局共享 RESTMapper。
//
// RESTMapper 初始化一次并在进程生命周期内缓存。
// 这是安全的，因为：
// - BKE CRD 在控制器启动前安装（通过 Helm/kubectl）
// - CRD 在控制器运行期间不会改变
// - RESTMapper 缓存在进程生命周期内是稳定的
//
// 如果将来需要在运行时动态安装/删除 CRD，
// 可以在那时添加缓存失效机制。
func GetGlobalRESTMapper(config *rest.Config) (meta.RESTMapper, error) {
    globalRESTMapperOnce.Do(func() {
        discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
        if err != nil {
            globalRESTMapperErr = err
            return
        }
        
        // 使用内存缓存
        cachedDiscovery := memory.NewMemCacheClient(discoveryClient)
        
        // 创建延迟发现的 RESTMapper
        globalRESTMapper = restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery)
    })
    
    return globalRESTMapper, globalRESTMapperErr
}
```

#### 4. 使用层更新

**文件：`pkg/kube/kube.go`**

```go
// 修改 NewClientFromRestConfig (L119-144)
func NewClientFromRestConfig(ctx context.Context, config *rest.Config) (RemoteKubeClient, error) {
    // 使用工厂方法（QPS/Burst 已在 ApplyThrottlingConfig 中设置）
    clientSet, err := NewClientFromConfig(config)
    if err != nil {
        return nil, errors.Wrap(err, "failed to create cluster clientset")
    }
    
    dynamicClient, err := NewDynamicClientFromConfig(config)
    if err != nil {
        return nil, errors.Wrap(err, "failed to create remote cluster dynamicClient")
    }
    
    // ... 其余代码不变 ...
}
```

**文件：`cmd/capbke/main.go`**

```go
// 修改 createManager 函数 (L185)
func createManager() (ctrl.Manager, *remote.ClusterCacheTracker) {
    // ... 现有代码 ...
    
    // 使用工厂方法获取配置（QPS/Burst 已应用）
    mgr, err := ctrl.NewManager(GetManagerConfig(), ctrl.Options{
        Scheme:                 scheme,
        MetricsBindAddress:     config.MetricsAddr,
        // ... 其余选项不变 ...
    })
    
    // ... 其余代码不变 ...
}
```

**文件：`cmd/bkeagent/main.go`**

```go
// 修改 newManager 函数 (L104)
func newManager() (ctrl.Manager, error) {
    // 使用工厂方法获取配置（QPS/Burst 已应用）
    return ctrl.NewManager(GetManagerConfig(), ctrl.Options{
        Scheme:             scheme,
        // ... 其余选项不变 ...
    })
}
```

**文件：`pkg/kube/wait.go`**

```go
// 修改 ToRESTMapper (L187-195)
func (f *kubeFactory) ToRESTMapper() (meta.RESTMapper, error) {
    // 使用全局共享的 RESTMapper
    mapper, err := GetGlobalRESTMapper(f.config)
    if err != nil {
        return nil, err
    }
    
    // 创建 ShortcutExpander（轻量级，可以每次创建）
    discoveryClient, err := f.ToDiscoveryClient()
    if err != nil {
        return nil, err
    }
    expander := restmapper.NewShortcutExpander(mapper, discoveryClient)
    return expander, nil
}
```

**文件：`pkg/kube/helm.go`**

```go
// 修改 ToRESTMapper (L56-64)
func (r *RestClientConfig) ToRESTMapper() (meta.RESTMapper, error) {
    // 使用全局共享的 RESTMapper
    mapper, err := GetGlobalRESTMapper(r.restConfig)
    if err != nil {
        return nil, fmt.Errorf("failed to get global REST mapper: %v", err)
    }
    
    // 创建 ShortcutExpander
    c, err := r.ToDiscoveryClient()
    if err != nil {
        return nil, fmt.Errorf("failed to create discovery client: %v", err)
    }
    se := restmapper.NewShortcutExpander(mapper, c)
    return se, nil
}
```

### 测试计划

#### 单元测试

**文件：`pkg/kube/client_factory_test.go`**

```go
func TestApplyThrottlingConfig(t *testing.T) {
    tests := []struct {
        name          string
        inputConfig   *rest.Config
        expectedQPS   float32
        expectedBurst int
    }{
        {
            name:          "nil config returns nil",
            inputConfig:   nil,
            expectedQPS:   0,
            expectedBurst: 0,
        },
        {
            name: "applies default values",
            inputConfig: &rest.Config{
                Host: "https://localhost:6443",
            },
            expectedQPS:   50,
            expectedBurst: 100,
        },
        {
            name: "overrides existing values",
            inputConfig: &rest.Config{
                Host:  "https://localhost:6443",
                QPS:   10,
                Burst: 20,
            },
            expectedQPS:   50,
            expectedBurst: 100,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := ApplyThrottlingConfig(tt.inputConfig)
            if tt.inputConfig == nil {
                assert.Nil(t, result)
                return
            }
            assert.Equal(t, tt.expectedQPS, result.QPS)
            assert.Equal(t, tt.expectedBurst, result.Burst)
        })
    }
}
```

**文件：`pkg/kube/restmapper_test.go`**

```go
func TestGetGlobalRESTMapper(t *testing.T) {
    config := &rest.Config{
        Host: "https://localhost:6443",
    }
    
    // 第一次调用
    mapper1, err := GetGlobalRESTMapper(config)
    require.NoError(t, err)
    require.NotNil(t, mapper1)
    
    // 第二次调用应该返回相同实例（验证单例）
    mapper2, err := GetGlobalRESTMapper(config)
    require.NoError(t, err)
    assert.Equal(t, mapper1, mapper2, "Should return same instance")
    
    // 验证 RESTMapper 可以查询 API 资源
    _, err = mapper1.RESTMapping(schema.GroupKind{Group: "*", Kind: ""})
    require.NoError(t, err, "RESTMapper should be able to query API resources")
}
```

#### 集成测试

**文件：`test/integration/performance_test.go`**

```go
func TestAPIDiscoveryPerformance(t *testing.T) {
    config := ctrl.GetConfigOrDie()
    config.QPS = 50
    config.Burst = 100
    
    start := time.Now()
    
    // 执行 API 资源发现
    mapper, err := GetGlobalRESTMapper(config)
    require.NoError(t, err)
    
    // 查询所有 APIGroup
    _, err = mapper.RESTMapping(schema.GroupKind{Group: "*", Kind: ""})
    require.NoError(t, err)
    
    elapsed := time.Since(start)
    
    // 验证性能
    assert.Less(t, elapsed, 30*time.Second, "API discovery should complete within 30s")
    t.Logf("API discovery completed in %v", elapsed)
}
```

#### 端到端测试

```bash
# 启动 BKE 控制器
kubectl apply -f bke-controller-manager.yaml

# 观察日志，验证限流警告是否减少
kubectl logs -f -n bke-system deployment/bke-controller-manager | grep "client-side throttling"

# 预期：限流警告显著减少或消除

# 创建 64 节点集群
kubectl apply -f bkecluster-64n.yaml

# 监控集群状态
watch -n 5 'kubectl get bkecluster bke-cluster-128n -o jsonpath="{.status.clusterStatus}"'

# 预期：总时间 < 20 分钟（从 29 分钟优化）
```

### 毕业标准

#### Alpha (v0.1)
- [ ] 实现集中化 QPS/Burst 配置
- [ ] 实现全局 RESTMapper 单例
- [ ] 单元测试通过
- [ ] 现有功能无回归

#### Beta (v0.2)
- [ ] 集成测试通过
- [ ] 性能测试显示 API 发现时间 < 30 秒
- [ ] 日志中无客户端限流警告
- [ ] API 服务器负载监控显示可接受的增加

#### Stable (v1.0)
- [ ] 在 64 节点集群上端到端测试通过
- [ ] 集群创建总时间 < 20 分钟
- [ ] 生产环境部署 1 个月无问题
- [ ] 文档已更新

### 升级/降级策略

**升级：**
- 现有部署无需配置更改
- 默认值（QPS=50，Burst=100）对大多数部署是安全的
- 用户可以根据需要通过命令行标志或环境变量覆盖

**降级：**
- 恢复到以前的版本将恢复默认的 client-go 限流行为
- 预计不会丢失数据或状态损坏

## 实施历史

- **2026-07-15**：创建初始 KEP
- **待定**：Alpha 实现
- **待定**：Beta 实现
- **待定**：Stable 发布

## 缺点

1. **API 服务器负载增加**：QPS 从 5 增加到 50 将使管理集群 API 服务器的请求速率增加 10 倍。如果 API 服务器大小不合适，这可能会使 API 服务器过载。
   - **缓解措施**：部署后监控 API 服务器指标（请求延迟、队列长度）。提供配置选项以根据需要调整 QPS/Burst。

2. **缓存失效复杂性**：全局 RESTMapper 单例不支持缓存失效。如果 CRD 在运行时动态安装/删除，缓存可能会变得陈旧。
   - **缓解措施**：记录此限制。如果需要动态 CRD 管理，在将来的 KEP 中添加缓存失效机制。

3. **线程安全问题**：单例模式需要小心的线程安全实现。
   - **缓解措施**：使用 `sync.Once` 进行线程安全初始化。使用并发访问模式进行广泛测试。

## 替代方案

### 替代方案 1：仅增加 QPS/Burst（不使用 RESTMapper 缓存）

**优点：**
- 实现更简单
- 代码更改更少

**缺点：**
- 每次调用仍会执行 API 发现
- 没有解决冗余发现调用的根本原因

**决定：** 拒绝。RESTMapper 缓存提供了额外的 2 分钟改进，值得实现。

### 替代方案 2：按需 API 发现

只查询控制器实际需要的 API 组，而不是发现所有可用的 API 组。

**优点：**
- 减少 API 请求总数
- 更有针对性的方法

**缺点：**
- 需要识别需要哪些 API 组
- 可能会遗漏将来需要的 API 组
- 实现更复杂

**决定：** 拒绝。QPS/Burst 增加和 RESTMapper 缓存的组合更简单有效。

### 替代方案 3：使用 controller-runtime 的内置缓存

利用 controller-runtime 的内置缓存机制，而不是实现自定义 RESTMapper 缓存。

**优点：**
- 使用框架提供的解决方案
- 需要维护的自定义代码更少

**缺点：**
- controller-runtime 的缓存可能不足以满足我们的用例
- 对缓存行为的控制较少

**决定：** 拒绝。自定义 RESTMapper 单例提供更好的控制和经过验证的性能改进。

## 所需基础设施

1. **性能测试环境**：用于端到端性能测试的 64 节点集群
2. **监控**：API 服务器指标监控（请求延迟、队列长度、CPU/内存使用率）
3. **负载测试工具**：用于模拟 API 服务器负载和测量客户端性能的工具

## 参考资料

1. [Kubernetes client-go 速率限制](https://github.com/kubernetes/client-go/blob/master/rest/request.go)
2. [controller-runtime Manager 配置](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/manager)
3. [BKE 64 节点集群性能分析报告](../../performance/report/64节点集群性能瓶颈分析与优化方案.md)

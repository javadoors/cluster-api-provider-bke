# 消除 BKE 控制器 API 限流问题

## 摘要

本提案提出消除 BKE 控制器中的 API 限流瓶颈，该瓶颈当前导致集群创建过程中出现 9 分 37 秒的延迟。限流发生的原因是 Kubernetes client-go 库使用保守的默认限流设置（QPS=5，Burst=10），这些设置无法满足 BKE 控制器的工作负载需求。

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

1. 优化 API 服务器端性能（不在本提案范围内）
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
│                      配置层                                 │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  utils/capbke/config/config.go                       │   │
│  │  - ClientQPS (默认: 50)                              │   │
│  │  - ClientBurst (默认: 100)                           │   │
│  │  - 支持命令行标志和环境变量                            │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│                      工厂层                                 │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/client_factory.go                          │   │
│  │  - ApplyThrottlingConfig(config)                     │   │
│  │  - NewClientFromConfig(config)                       │   │
│  │  - NewDynamicClientFromConfig(config)                │   │
│  │  - GetManagerConfig()                                │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│                      使用层                                  │
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
│                全局 RESTMapper 单例                          │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/restmapper.go                              │   │
│  │  - globalRESTMapper (单例)                            │   │
│  │  - sync.Once 用于线程安全初始化                        │   │
│  │  - memory.NewMemCacheClient 用于缓存                  │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│                      使用模式                                │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/wait.go                                    │   │
│  │  - ToRESTMapper() 调用 GetGlobalRESTMapper()         │   │
│  │  - 第一次调用：初始化并缓存                            │   │
│  │  - 后续调用：返回缓存的实例                            │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/helm.go                                    │   │
│  │  - ToRESTMapper() 调用 GetGlobalRESTMapper()         │   │
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

本提案不引入任何新的 CRD 或 API 变更。所有更改都是控制器实现的内部更改。

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

## 设计视图

### 1.1 系统架构总览

```mermaid
graph TB
    subgraph "BKE Controller"
        A[配置层<br/>config.go] --> B[工厂层<br/>client_factory.go]
        B --> C[使用层<br/>kube.go, main.go]
        B --> D[RESTMapper 单例<br/>restmapper.go]
    end
    
    subgraph "Kubernetes API Server"
        E[API Server<br/>管理集群]
    end
    
    C --> E
    D --> E
    
    style A fill:#e1f5ff
    style B fill:#fff4e1
    style D fill:#e8f5e9
```

**组件职责说明：**

| 组件 | 职责 | 文件位置 |
|------|------|---------|
| **配置层** | 管理 QPS/Burst 配置，支持命令行标志和环境变量 | `utils/capbke/config/config.go` |
| **工厂层** | 提供统一的客户端创建方法，自动应用限流配置 | `pkg/kube/client_factory.go` |
| **RESTMapper 单例** | 全局共享的 RESTMapper，带内存缓存 | `pkg/kube/restmapper.go` |
| **使用层** | 调用工厂方法创建客户端，使用全局 RESTMapper | `pkg/kube/kube.go`, `cmd/capbke/main.go` 等 |

### 1.2 优化前后对比时序图

```mermaid
sequenceDiagram
    participant Client as BKE Controller
    participant API as API Server
    
    Note over Client,API: 优化前（9分37秒）
    loop 60-90 次 API 发现
        Client->>API: GET /apis/...
        API-->>Client: 响应
        Note right of Client: QPS=5, Burst=10<br/>限流等待 1s→9s
    end
    
    Note over Client,API: 优化后（<30秒）
    Client->>API: GET /apis/... (首次)
    API-->>Client: 响应
    Note right of Client: 缓存到 RESTMapper
    loop 后续调用
        Client->>Client: 从缓存读取
        Note right of Client: 无网络调用<br/>QPS=50, Burst=100
    end
```

**性能对比：**

| 指标 | 优化前 | 优化后 | 提升 |
|------|--------|--------|------|
| API 发现时间 | 9 分 37 秒 | < 30 秒 | 95% |
| API 调用次数 | 60-90 次 | 1 次（首次） | 98% |
| 限流等待时间 | ~10 秒/次 | 0 | 100% |
| 集群创建总时间 | 29 分 29 秒 | ~18 分钟 | 39% |

### 1.3 组件交互图

```mermaid
graph LR
    subgraph "配置管理"
        A[命令行标志<br/>--client-qps] --> C[config.ClientQPS]
        B[环境变量<br/>KUBE_CLIENT_QPS] --> C
        D[默认值<br/>50] --> C
    end
    
    subgraph "客户端创建"
        C --> E[ApplyThrottlingConfig]
        E --> F[NewClientFromConfig]
        E --> G[NewDynamicClientFromConfig]
        E --> H[GetManagerConfig]
    end
    
    subgraph "RESTMapper 缓存"
        I[GetGlobalRESTMapper] --> J{首次调用?}
        J -->|是| K[创建 DiscoveryClient]
        K --> L[创建 MemCacheClient]
        L --> M[创建 DeferredDiscoveryRESTMapper]
        M --> N[缓存到 globalRESTMapper]
        J -->|否| O[返回 globalRESTMapper]
        N --> O
    end
    
    subgraph "使用层"
        P[kube.go] --> F
        P --> G
        Q[main.go] --> H
        R[wait.go] --> I
        S[helm.go] --> I
    end
```

### 1.4 数据流图

```mermaid
graph TD
    A[用户启动控制器] --> B{检查配置来源}
    B -->|命令行标志| C[解析 --client-qps]
    B -->|环境变量| D[读取 KUBE_CLIENT_QPS]
    B -->|默认值| E[使用 50]
    
    C --> F[config.ClientQPS = 用户值]
    D --> F
    E --> F
    
    F --> G[创建 Manager]
    G --> H[GetManagerConfig]
    H --> I[ApplyThrottlingConfig]
    I --> J[设置 cfg.QPS = config.ClientQPS]
    J --> K[设置 cfg.Burst = config.ClientBurst]
    K --> L[返回配置好的 rest.Config]
    
    L --> M[创建 Kubernetes Client]
    L --> N[创建 RESTMapper]
    
    N --> O{缓存存在?}
    O -->|是| P[返回缓存的 RESTMapper]
    O -->|否| Q[创建新的 RESTMapper]
    Q --> R[缓存到 globalRESTMapper]
    R --> P
    
    P --> S[执行 API 发现]
    M --> S
    S --> T[API Server]
```

### 1.5 部署视图

```mermaid
graph TB
    subgraph "管理集群"
        A[BKE Controller<br/>Deployment]
        B[API Server]
        C[etcd]
    end
    
    subgraph "配置注入"
        D[ConfigMap<br/>bke-controller-config]
        E[命令行参数<br/>--client-qps=50<br/>--client-burst=100]
        F[环境变量<br/>KUBE_CLIENT_QPS=50]
    end
    
    subgraph "监控"
        G[Prometheus<br/>指标采集]
        H[Grafana<br/>可视化]
        I[日志系统<br/>ELK/Loki]
    end
    
    D --> A
    E --> A
    F --> A
    A --> B
    B --> C
    
    A --> G
    G --> H
    A --> I
    
    style A fill:#fff4e1
    style B fill:#e1f5ff
    style G fill:#e8f5e9
```

**监控点说明：**

| 监控指标 | 采集方式 | 告警阈值 | 说明 |
|---------|---------|---------|------|
| API 发现时间 | Prometheus | > 30s | 优化后的预期时间 |
| 客户端限流次数 | 日志 | > 0 | 应该完全消除 |
| API Server 请求速率 | Prometheus | > 1000 QPS | 防止过载 |
| API Server 请求延迟 | Prometheus | P99 > 1s | 监控性能影响 |
| RESTMapper 缓存命中率 | 自定义指标 | < 95% | 验证缓存效果 |

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

## 工作量评估

### 1. 开发工作量

| 模块 | 任务 | 预估人天 | 优先级 | 依赖 |
|------|------|---------|--------|------|
| **配置层** | 添加 QPS/Burst 配置变量 | 0.5 | P0 | 无 |
| **配置层** | 实现命令行标志解析 | 0.5 | P0 | 配置变量 |
| **配置层** | 实现环境变量支持 | 0.5 | P1 | 配置变量 |
| **工厂层** | 实现 client_factory.go | 2 | P0 | 配置层 |
| **工厂层** | 添加错误处理和日志 | 0.5 | P1 | 工厂方法 |
| **RESTMapper** | 实现全局单例模式 | 1 | P0 | 无 |
| **RESTMapper** | 实现内存缓存逻辑 | 1 | P0 | 单例模式 |
| **使用层** | 修改 pkg/kube/kube.go | 0.5 | P0 | 工厂层 |
| **使用层** | 修改 cmd/capbke/main.go | 0.5 | P0 | 工厂层 |
| **使用层** | 修改 cmd/bkeagent/main.go | 0.5 | P0 | 工厂层 |
| **使用层** | 修改 pkg/kube/wait.go | 0.5 | P0 | RESTMapper |
| **使用层** | 修改 pkg/kube/helm.go | 0.5 | P0 | RESTMapper |
| **小计** | | **9** | | |

### 2. 测试工作量

| 测试类型 | 任务 | 预估人天 | 说明 |
|---------|------|---------|------|
| **单元测试** | client_factory_test.go | 1 | 覆盖所有工厂方法 |
| **单元测试** | restmapper_test.go | 1 | 验证单例和缓存 |
| **单元测试** | config_test.go | 0.5 | 验证配置加载 |
| **集成测试** | performance_test.go | 2 | API 发现性能测试 |
| **集成测试** | 并发访问测试 | 1 | 验证线程安全 |
| **端到端测试** | 64 节点集群测试 | 3 | 实际部署验证 |
| **性能基准** | 优化前后对比 | 2 | 生成性能报告 |
| **小计** | | **10.5** | |

### 3. 文档工作量

| 任务 | 预估人天 | 说明 |
|------|---------|------|
| 用户文档更新 | 1 | 配置说明、使用示例 |
| 运维手册更新 | 1 | 监控指标、故障排查 |
| API 文档更新 | 0.5 | 新增接口说明 |
| 发布说明 | 0.5 | 版本更新日志 |
| **小计** | **3** | |

### 4. 总工作量汇总

```mermaid
pie title 工作量分布
    "开发 (9人天)" : 9
    "测试 (10.5人天)" : 10.5
    "文档 (3人天)" : 3
```

| 类别 | 人天 | 占比 |
|------|------|------|
| 开发 | 9 | 40% |
| 测试 | 10.5 | 47% |
| 文档 | 3 | 13% |
| **总计** | **22.5** | **100%** |

**人力资源配置：**
- **方案 A**：1 名开发人员，约 4.5 周（22.5 人天 ÷ 5 天/周）
- **方案 B**：2 名开发人员，约 2.5 周（可并行开发）
- **推荐**：方案 B，缩短交付周期

### 5. 里程碑计划

```mermaid
gantt
    title 项目实施计划
    dateFormat  YYYY-MM-DD
    section 开发
    配置层实现           :a1, 2026-07-16, 2d
    工厂层实现           :a2, after a1, 2d
    RESTMapper 实现      :a3, after a2, 2d
    使用层修改           :a4, after a3, 2d
    section 测试
    单元测试             :b1, after a4, 2d
    集成测试             :b2, after b1, 3d
    端到端测试           :b3, after b2, 3d
    section 文档
    文档更新             :c1, after b3, 2d
```

| 里程碑 | 时间 | 交付物 | 验收标准 |
|--------|------|--------|---------|
| **M1: 核心功能** | Week 1 | 配置层 + 工厂层 | 单元测试通过，配置可加载 |
| **M2: RESTMapper** | Week 2 | 全局单例实现 | 缓存测试通过，线程安全验证 |
| **M3: 集成测试** | Week 3 | 性能测试报告 | API 发现 < 30s，无回归 |
| **M4: 生产就绪** | Week 4 | 端到端测试 + 文档 | 集群创建 < 20min，文档完整 |

### 6. 风险评估与缓冲

| 风险 | 概率 | 影响 | 缓解措施 | 预留缓冲 |
|------|------|------|---------|---------|
| API Server 过载 | 中 | 高 | 渐进式调优（5→20→50），监控指标 | +2 天 |
| 线程安全问题 | 低 | 高 | 并发测试，压力测试 | +1 天 |
| 性能未达预期 | 中 | 中 | 参数调优，架构优化 | +2 天 |
| 兼容性问题 | 低 | 中 | 多版本测试，回滚机制 | +1 天 |
| **总缓冲** | | | | **+6 天** |

**调整后的总工作量：**
- 基础工作量：22.5 人天
- 风险缓冲：6 人天
- **最终工作量：28.5 人天（约 5.7 周，1 名开发人员）**

### 7. 成本效益分析

| 指标 | 数值 | 说明 |
|------|------|------|
| **投入成本** | 28.5 人天 | 开发 + 测试 + 文档 + 缓冲 |
| **性能提升** | 节省 11 分钟/集群 | API 发现从 9m37s → <30s |
| **年化收益** | 节省 1,320 分钟 | 假设每天创建 2 个集群 |
| **投资回报率** | 约 46x | 1,320 分钟 ÷ 28.5 人天 ≈ 46 |

**结论：** 该优化具有高投资回报率，建议优先实施。

### 升级/降级策略

**升级：**
- 现有部署无需配置更改
- 默认值（QPS=50，Burst=100）对大多数部署是安全的
- 用户可以根据需要通过命令行标志或环境变量覆盖

**降级：**
- 恢复到以前的版本将恢复默认的 client-go 限流行为
- 预计不会丢失数据或状态损坏

## 缺点

1. **API 服务器负载增加**：QPS 从 5 增加到 50 将使管理集群 API 服务器的请求速率增加 10 倍。如果 API 服务器大小不合适，这可能会使 API 服务器过载。
   - **缓解措施**：部署后监控 API 服务器指标（请求延迟、队列长度）。提供配置选项以根据需要调整 QPS/Burst。

2. **缓存失效复杂性**：全局 RESTMapper 单例不支持缓存失效。如果 CRD 在运行时动态安装/删除，缓存可能会变得陈旧。
   - **缓解措施**：记录此限制。如果需要动态 CRD 管理，在将来的 KEP 中添加缓存失效机制。

3. **线程安全问题**：单例模式需要小心的线程安全实现。
   - **缓解措施**：使用 `sync.Once` 进行线程安全初始化。使用并发访问模式进行广泛测试。

## 所需基础设施

1. **性能测试环境**：用于端到端性能测试的 64 节点集群
2. **监控**：API 服务器指标监控（请求延迟、队列长度、CPU/内存使用率）
3. **负载测试工具**：用于模拟 API 服务器负载和测量客户端性能的工具

## 参考资料

1. [Kubernetes client-go 速率限制](https://github.com/kubernetes/client-go/blob/master/rest/request.go)
2. [controller-runtime Manager 配置](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/manager)

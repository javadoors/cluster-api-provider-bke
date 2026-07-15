# KEP-10: Eliminate API Throttling in BKE Controller

<!--
This is a template for Kubernetes Enhancement Proposals (KEPs).
See the KEP template for more information:
https://github.com/kubernetes/enhancements/blob/master/keps/NNN-template/README.md
-->

## Metadata

| Field | Value |
|-------|-------|
| **KEP** | 10 |
| **Title** | Eliminate API Throttling in BKE Controller |
| **Status** | Provisional |
| **Creation Date** | 2026-07-15 |
| **Last Updated** | 2026-07-15 |
| **Authors** | BKE Team |
| **Reviewers** | TBD |
| **Approvers** | TBD |
| **SIG** | bke-performance |
| **Sponsor SIG** | bke-performance |

## Summary

This KEP proposes to eliminate the API throttling bottleneck in the BKE controller, which currently causes a 9-minute 37-second delay during cluster creation. The throttling occurs because the Kubernetes client-go library uses conservative default rate-limiting settings (QPS=5, Burst=10) that are insufficient for the BKE controller's workload.

The solution involves two complementary optimizations:
1. **Centralized QPS/Burst Configuration**: Increase client rate limits from QPS=5/Burst=10 to QPS=50/Burst=100 through a unified configuration management system
2. **RESTMapper Caching**: Implement a global singleton RESTMapper with memory caching to eliminate redundant API discovery calls

These changes will reduce the API discovery phase from 9 minutes 37 seconds to approximately 30 seconds, representing a 95% improvement and saving 11 minutes in the overall cluster creation process.

## Motivation

### Why is this needed?

The BKE controller experiences severe API throttling during cluster creation, which is the single largest bottleneck in the cluster provisioning workflow. This throttling occurs during the API resource discovery phase when the controller needs to query all available API groups and their versions from the management cluster's API server.

### What problem does it solve?

**Current Performance Data (64-node cluster):**
- Total cluster creation time: 29 minutes 29 seconds
- API throttling duration: 9 minutes 37 seconds (32.6% of total time)
- Throttling events: 57 occurrences
- Average wait time per request: ~10 seconds
- Maximum wait time: 9.20 seconds

**Root Cause Analysis:**
The throttling is caused by the default client-go rate limiter configuration:
- Default QPS: 5 requests per second
- Default Burst: 10 requests
- Actual workload: 60-90 API discovery requests (30+ API groups × 2-3 versions each)

With these settings, the first 10 requests are sent immediately (burst), but subsequent requests are throttled with exponentially increasing delays (1s → 2s → 3s → ... → 9s), resulting in a total wait time of nearly 10 minutes.

**Impact:**
- User experience: 10 minutes of no visible progress during cluster creation
- Resource waste: Pure waiting time with no actual deployment work
- Scalability limitation: The problem worsens as more CRDs are registered in the management cluster

### Measurable Goals

1. Reduce API discovery time from 9 minutes 37 seconds to less than 30 seconds (95% improvement)
2. Eliminate client-side throttling warnings in controller logs
3. Reduce total cluster creation time from 29 minutes 29 seconds to approximately 18 minutes (39% improvement)
4. Maintain API server stability with increased client request rates

### Non-Goals

1. Optimize API server-side performance (out of scope for this KEP)
2. Reduce the number of API groups in the management cluster
3. Implement distributed caching across multiple controller instances
4. Modify the controller-runtime framework's default behavior

## Proposal

### User Stories

**Story 1: Fast Cluster Creation**
As a cluster operator, I want to create a 64-node Kubernetes cluster in less than 20 minutes so that I can respond quickly to capacity demands.

*Current state:* Cluster creation takes 29+ minutes, with 10 minutes of pure API throttling delay.
*Desired state:* Cluster creation completes in under 20 minutes with no throttling delays.

**Story 2: Predictable Performance**
As a cluster operator, I want consistent cluster creation times regardless of how many CRDs are registered in the management cluster.

*Current state:* Each additional API group adds ~10-20 seconds to the discovery phase.
*Desired state:* API discovery time remains constant regardless of the number of registered CRDs.

**Story 3: Operational Visibility**
As a cluster operator, I want to see continuous progress during cluster creation so that I can identify and troubleshoot issues early.

*Current state:* No progress is shown for the first 10 minutes due to API throttling.
*Desired state:* Progress is visible from the start of cluster creation.

### Notes/Constraints

1. **API Server Load**: Increasing QPS from 5 to 50 will increase request rate to the management cluster's API server by 10x. This must be monitored to ensure the API server can handle the increased load.

2. **Backward Compatibility**: The changes must not break existing deployments or require configuration changes for existing users.

3. **Configuration Flexibility**: Different deployment scenarios may require different QPS/Burst settings. The solution must support runtime configuration through command-line flags and environment variables.

4. **Thread Safety**: The global RESTMapper singleton must be thread-safe for concurrent access from multiple goroutines.

### Implementation Approach

The solution consists of two complementary optimizations:

#### Optimization A: Centralized QPS/Burst Configuration

**Architecture:**
```
┌─────────────────────────────────────────────────────────────┐
│                    Configuration Layer                       │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  utils/capbke/config/config.go                        │   │
│  │  - ClientQPS (default: 50)                            │   │
│  │  - ClientBurst (default: 100)                         │   │
│  │  - Support command-line flags and env vars            │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│                    Factory Layer                             │
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
│                    Usage Layer                               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐       │
│  │ pkg/kube/    │  │ cmd/capbke/  │  │ cmd/bkeagent/│       │
│  │ kube.go      │  │ main.go      │  │ main.go      │       │
│  │              │  │              │  │              │       │
│  │ Use factory  │  │ Use factory  │  │ Use factory  │       │
│  └──────────────┘  └──────────────┘  └──────────────┘       │
└─────────────────────────────────────────────────────────────┘
```

**Configuration Priority:**
```
Command-line flags > Environment variables > Default values

Examples:
1. Command-line: --client-qps=100 --client-burst=200
2. Environment: KUBE_CLIENT_QPS=80 KUBE_CLIENT_BURST=160
3. Defaults: QPS=50, Burst=100
```

#### Optimization B: Global RESTMapper Singleton with Caching

**Architecture:**
```
┌─────────────────────────────────────────────────────────────┐
│              Global RESTMapper Singleton                     │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/restmapper.go                               │   │
│  │  - globalRESTMapper (singleton)                       │   │
│  │  - sync.Once for thread-safe initialization           │   │
│  │  - memory.NewMemCacheClient for caching               │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│                    Usage Pattern                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/wait.go                                     │   │
│  │  - ToRESTMapper() calls GetGlobalRESTMapper()         │   │
│  │  - First call: initializes and caches                 │   │
│  │  - Subsequent calls: returns cached instance          │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  pkg/kube/helm.go                                     │   │
│  │  - ToRESTMapper() calls GetGlobalRESTMapper()         │   │
│  │  - Same caching behavior as wait.go                   │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

**Cache Invalidation Strategy:**
The RESTMapper cache is stable for the lifetime of the process because:
- BKE CRDs are installed before the controller starts (via Helm/kubectl)
- CRDs do not change during controller runtime
- If dynamic CRD installation/deletion is needed in the future, a cache invalidation mechanism can be added

## Design Details

### API Changes

This KEP does not introduce any new CRDs or API changes. All changes are internal to the controller implementation.

### Code Changes

#### 1. Configuration Layer

**File: `utils/capbke/config/config.go`**

```go
var (
    // ... existing configuration ...
    
    // ClientQPS is the QPS for Kubernetes client
    // Default: 50, can be overridden by --client-qps flag or KUBE_CLIENT_QPS env var
    ClientQPS float32
    
    // ClientBurst is the burst size for Kubernetes client
    // Default: 100, can be overridden by --client-burst flag or KUBE_CLIENT_BURST env var
    ClientBurst int
)

const (
    // DefaultClientQPS is the default QPS for Kubernetes client
    DefaultClientQPS = 50
    // DefaultClientBurst is the default burst size for Kubernetes client
    DefaultClientBurst = 100
)

func ConfigurationFlag() {
    // ... existing configuration ...
    
    flag.Float32Var(&ClientQPS, "client-qps", DefaultClientQPS,
        "QPS for Kubernetes client. Default: 50. Can also be set via KUBE_CLIENT_QPS env var")
    flag.IntVar(&ClientBurst, "client-burst", DefaultClientBurst,
        "Burst size for Kubernetes client. Default: 100. Can also be set via KUBE_CLIENT_BURST env var")
}

func init() {
    // Read from environment variables
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
    
    // Use defaults if not set
    if ClientQPS == 0 {
        ClientQPS = DefaultClientQPS
    }
    if ClientBurst == 0 {
        ClientBurst = DefaultClientBurst
    }
}
```

#### 2. Factory Layer

**File: `pkg/kube/client_factory.go` (new file)**

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

// ApplyThrottlingConfig applies QPS/Burst throttling configuration to rest.Config
// This is the single source of truth for client throttling settings
func ApplyThrottlingConfig(cfg *rest.Config) *rest.Config {
    if cfg == nil {
        return cfg
    }
    
    cfg.QPS = config.ClientQPS
    cfg.Burst = config.ClientBurst
    
    return cfg
}

// NewClientFromConfig creates a new Kubernetes client with throttling applied
func NewClientFromConfig(cfg *rest.Config) (*kubernetes.Clientset, error) {
    cfg = ApplyThrottlingConfig(cfg)
    return kubernetes.NewForConfig(cfg)
}

// NewDynamicClientFromConfig creates a new dynamic client with throttling applied
func NewDynamicClientFromConfig(cfg *rest.Config) (dynamic.Interface, error) {
    cfg = ApplyThrottlingConfig(cfg)
    return dynamic.NewForConfig(cfg)
}

// GetManagerConfig returns a rest.Config for controller-runtime manager with throttling applied
func GetManagerConfig() *rest.Config {
    return ApplyThrottlingConfig(ctrl.GetConfigOrDie())
}

// NewRemoteKubeClient creates a RemoteKubeClient with throttling applied
func NewRemoteKubeClient(ctx context.Context, cfg *rest.Config) (RemoteKubeClient, error) {
    return NewClientFromRestConfig(ctx, ApplyThrottlingConfig(cfg))
}
```

#### 3. RESTMapper Singleton

**File: `pkg/kube/restmapper.go` (new file)**

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

// GetGlobalRESTMapper returns a global shared RESTMapper with caching.
//
// The RESTMapper is initialized once and cached for the lifetime of the process.
// This is safe because:
// - BKE CRDs are installed before the controller starts (via Helm/kubectl)
// - CRDs do not change during controller runtime
// - RESTMapper cache is stable for the lifetime of the process
//
// If CRDs need to be dynamically installed/deleted during runtime in the future,
// a cache invalidation mechanism can be added at that time.
func GetGlobalRESTMapper(config *rest.Config) (meta.RESTMapper, error) {
    globalRESTMapperOnce.Do(func() {
        discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
        if err != nil {
            globalRESTMapperErr = err
            return
        }
        
        // Use memory cache
        cachedDiscovery := memory.NewMemCacheClient(discoveryClient)
        
        // Create deferred discovery RESTMapper
        globalRESTMapper = restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery)
    })
    
    return globalRESTMapper, globalRESTMapperErr
}
```

#### 4. Usage Layer Updates

**File: `pkg/kube/kube.go`**

```go
// Modify NewClientFromRestConfig (L119-144)
func NewClientFromRestConfig(ctx context.Context, config *rest.Config) (RemoteKubeClient, error) {
    // Use factory methods (QPS/Burst already set in ApplyThrottlingConfig)
    clientSet, err := NewClientFromConfig(config)
    if err != nil {
        return nil, errors.Wrap(err, "failed to create cluster clientset")
    }
    
    dynamicClient, err := NewDynamicClientFromConfig(config)
    if err != nil {
        return nil, errors.Wrap(err, "failed to create remote cluster dynamicClient")
    }
    
    // ... rest of the code unchanged ...
}
```

**File: `cmd/capbke/main.go`**

```go
// Modify createManager function (L185)
func createManager() (ctrl.Manager, *remote.ClusterCacheTracker) {
    // ... existing code ...
    
    // Use factory method to get config (QPS/Burst already applied)
    mgr, err := ctrl.NewManager(GetManagerConfig(), ctrl.Options{
        Scheme:                 scheme,
        MetricsBindAddress:     config.MetricsAddr,
        // ... rest of options unchanged ...
    })
    
    // ... rest of the code unchanged ...
}
```

**File: `cmd/bkeagent/main.go`**

```go
// Modify newManager function (L104)
func newManager() (ctrl.Manager, error) {
    // Use factory method to get config (QPS/Burst already applied)
    return ctrl.NewManager(GetManagerConfig(), ctrl.Options{
        Scheme:             scheme,
        // ... rest of options unchanged ...
    })
}
```

**File: `pkg/kube/wait.go`**

```go
// Modify ToRESTMapper (L187-195)
func (f *kubeFactory) ToRESTMapper() (meta.RESTMapper, error) {
    // Use global shared RESTMapper
    mapper, err := GetGlobalRESTMapper(f.config)
    if err != nil {
        return nil, err
    }
    
    // Create ShortcutExpander (lightweight, can be created each time)
    discoveryClient, err := f.ToDiscoveryClient()
    if err != nil {
        return nil, err
    }
    expander := restmapper.NewShortcutExpander(mapper, discoveryClient)
    return expander, nil
}
```

**File: `pkg/kube/helm.go`**

```go
// Modify ToRESTMapper (L56-64)
func (r *RestClientConfig) ToRESTMapper() (meta.RESTMapper, error) {
    // Use global shared RESTMapper
    mapper, err := GetGlobalRESTMapper(r.restConfig)
    if err != nil {
        return nil, fmt.Errorf("failed to get global REST mapper: %v", err)
    }
    
    // Create ShortcutExpander
    c, err := r.ToDiscoveryClient()
    if err != nil {
        return nil, fmt.Errorf("failed to create discovery client: %v", err)
    }
    se := restmapper.NewShortcutExpander(mapper, c)
    return se, nil
}
```

### Test Plan

#### Unit Tests

**File: `pkg/kube/client_factory_test.go`**

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

**File: `pkg/kube/restmapper_test.go`**

```go
func TestGetGlobalRESTMapper(t *testing.T) {
    config := &rest.Config{
        Host: "https://localhost:6443",
    }
    
    // First call
    mapper1, err := GetGlobalRESTMapper(config)
    require.NoError(t, err)
    require.NotNil(t, mapper1)
    
    // Second call should return same instance (verify singleton)
    mapper2, err := GetGlobalRESTMapper(config)
    require.NoError(t, err)
    assert.Equal(t, mapper1, mapper2, "Should return same instance")
    
    // Verify RESTMapper can query API resources
    _, err = mapper1.RESTMapping(schema.GroupKind{Group: "*", Kind: ""})
    require.NoError(t, err, "RESTMapper should be able to query API resources")
}
```

#### Integration Tests

**File: `test/integration/performance_test.go`**

```go
func TestAPIDiscoveryPerformance(t *testing.T) {
    config := ctrl.GetConfigOrDie()
    config.QPS = 50
    config.Burst = 100
    
    start := time.Now()
    
    // Execute API resource discovery
    mapper, err := GetGlobalRESTMapper(config)
    require.NoError(t, err)
    
    // Query all APIGroups
    _, err = mapper.RESTMapping(schema.GroupKind{Group: "*", Kind: ""})
    require.NoError(t, err)
    
    elapsed := time.Since(start)
    
    // Verify performance
    assert.Less(t, elapsed, 30*time.Second, "API discovery should complete within 30s")
    t.Logf("API discovery completed in %v", elapsed)
}
```

#### End-to-End Tests

```bash
# Start BKE controller
kubectl apply -f bke-controller-manager.yaml

# Observe logs, verify throttling warnings are reduced
kubectl logs -f -n bke-system deployment/bke-controller-manager | grep "client-side throttling"

# Expected: throttling warnings significantly reduced or eliminated

# Create 64-node cluster
kubectl apply -f bkecluster-64n.yaml

# Monitor cluster status
watch -n 5 'kubectl get bkecluster bke-cluster-128n -o jsonpath="{.status.clusterStatus}"'

# Expected: total time < 20 minutes (optimized from 29 minutes)
```

### Graduation Criteria

#### Alpha (v0.1)
- [ ] Implement centralized QPS/Burst configuration
- [ ] Implement global RESTMapper singleton
- [ ] Unit tests pass
- [ ] No regression in existing functionality

#### Beta (v0.2)
- [ ] Integration tests pass
- [ ] Performance test shows API discovery time < 30 seconds
- [ ] No client-side throttling warnings in logs
- [ ] API server load monitoring shows acceptable increase

#### Stable (v1.0)
- [ ] End-to-end tests pass on 64-node cluster
- [ ] Total cluster creation time < 20 minutes
- [ ] Production deployment for 1 month with no issues
- [ ] Documentation updated

### Upgrade / Downgrade Strategy

**Upgrade:**
- No configuration changes required for existing deployments
- Default values (QPS=50, Burst=100) are safe for most deployments
- Users can override via command-line flags or environment variables if needed

**Downgrade:**
- Reverting to previous version will restore default client-go throttling behavior
- No data loss or state corruption expected

## Implementation History

- **2026-07-15**: Initial KEP created
- **TBD**: Alpha implementation
- **TBD**: Beta implementation
- **TBD**: Stable release

## Drawbacks

1. **Increased API Server Load**: The QPS increase from 5 to 50 will increase request rate to the management cluster's API server by 10x. This could potentially overload the API server if it's not properly sized.
   - **Mitigation**: Monitor API server metrics (request latency, queue length) after deployment. Provide configuration options to adjust QPS/Burst if needed.

2. **Cache Invalidation Complexity**: The global RESTMapper singleton does not support cache invalidation. If CRDs are dynamically installed/deleted during runtime, the cache may become stale.
   - **Mitigation**: Document this limitation. Add cache invalidation mechanism in a future KEP if dynamic CRD management is needed.

3. **Thread Safety Concerns**: The singleton pattern requires careful thread safety implementation.
   - **Mitigation**: Use `sync.Once` for thread-safe initialization. Extensive testing with concurrent access patterns.

## Alternatives

### Alternative 1: Only Increase QPS/Burst (Without RESTMapper Caching)

**Pros:**
- Simpler implementation
- Fewer code changes

**Cons:**
- Still performs API discovery on every call
- Does not address the root cause of redundant discovery calls

**Decision:** Rejected. The RESTMapper caching provides additional 2-minute improvement and is worth the implementation effort.

### Alternative 2: On-Demand API Discovery

Only query API groups that are actually needed by the controller, rather than discovering all available API groups.

**Pros:**
- Reduces total number of API requests
- More targeted approach

**Cons:**
- Requires identifying which API groups are needed
- May miss API groups that are needed in the future
- More complex implementation

**Decision:** Rejected. The combination of QPS/Burst increase and RESTMapper caching is simpler and more effective.

### Alternative 3: Use controller-runtime's Built-in Caching

Leverage controller-runtime's built-in caching mechanisms instead of implementing a custom RESTMapper cache.

**Pros:**
- Uses framework-provided solution
- Less custom code to maintain

**Cons:**
- controller-runtime's caching may not be sufficient for our use case
- Less control over caching behavior

**Decision:** Rejected. The custom RESTMapper singleton provides better control and proven performance improvement.

## Infrastructure Needed

1. **Performance Test Environment**: A 64-node cluster for end-to-end performance testing
2. **Monitoring**: API server metrics monitoring (request latency, queue length, CPU/memory usage)
3. **Load Testing Tools**: Tools to simulate API server load and measure client performance

## References

1. [Kubernetes client-go Rate Limiting](https://github.com/kubernetes/client-go/blob/master/rest/request.go)
2. [controller-runtime Manager Configuration](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/manager)
3. [BKE 64-Node Cluster Performance Analysis Report](../../performance/report/64节点集群性能瓶颈分析与优化方案.md)

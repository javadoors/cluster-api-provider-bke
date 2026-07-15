# KEP-11: 优化健康检查收敛时间

<!--
This is a template for Kubernetes Enhancement Proposals (KEPs).
See the KEP template for more information:
https://github.com/kubernetes/enhancements/blob/master/keps/NNN-template/README.md
-->

## Metadata

| Field | Value |
|-------|-------|
| **KEP** | 11 |
| **Title** | 优化健康检查收敛时间 |
| **Status** | Provisional |
| **Creation Date** | 2026-07-15 |
| **Last Updated** | 2026-07-15 |
| **Authors** | BKE Team |
| **Reviewers** | TBD |
| **Approvers** | TBD |
| **SIG** | bke-performance |
| **Sponsor SIG** | bke-performance |

## Summary

本 KEP 旨在优化 BKE 集群创建过程中的健康检查收敛时间，将其从当前的 7 分 14 秒降低到 3 分钟以内，提升约 57%。

当前健康检查存在以下问题：
1. **串行检查**：所有节点和组件串行检查，耗时长
2. **无优先级**：关键组件和非关键组件同等对待
3. **固定间隔**：RequeueAfter 固定为 10 秒，无法根据失败原因动态调整
4. **无缓存**：每次检查都重新获取所有 Pod 状态，API 调用频繁
5. **Master NotReady**：Calico 部署后 Master 节点反复 NotReady，导致健康检查失败

解决方案包括：
1. **渐进式检查**：按优先级分阶段检查，关键组件失败立即返回
2. **并行化检查**：每个阶段内使用并行检查
3. **缓存机制**：使用缓存减少 API 调用
4. **动态间隔**：根据检查结果动态调整下次检查间隔
5. **Calico 优化**：修复 Calico 部署导致的 Master NotReady 问题

## Motivation

### Why is this needed?

健康检查收敛是 BKE 集群创建过程中的第二大性能瓶颈，占总耗时的 24.5%。在 64 节点集群的测试中，健康检查阶段耗时 7 分 14 秒，期间出现 33 次 ClusterUnhealthy 警告，Master 节点反复 NotReady。

### What problem does it solve?

**当前性能数据（64 节点集群）：**
- 健康检查收敛时间：7 分 14 秒
- ClusterUnhealthy 次数：33 次
- Master NotReady 次数：3 次（m1, m2, m3 各 1 次）
- 关键阻塞组件：metrics-server, openfuyao-system-controller

**根因分析：**

1. **Master NotReady 问题**
   - Calico 部署后 4-7 分钟，Master 节点依次 NotReady
   - 异常组件：calico-node, etcd, kube-apiserver, kube-controller-manager
   - 每次异常持续 30-60 秒后自动恢复
   - 因果关系：Calico 未部署时 Master 节点 Ready，部署后出现 NotReady

2. **关键组件长时间 Pending**
   - openfuyao-system-controller：Pending 总时长约 7 分钟
   - metrics-server：Pending 总时长约 7 分钟
   - 原因：镜像拉取慢、调度延迟、依赖组件未就绪

3. **健康检查机制问题**
   - 串行检查所有节点和组件
   - 无优先级区分
   - 固定 10 秒重试间隔
   - 无缓存机制，API 调用频繁

**影响：**
- 用户体验：集群创建最后 7 分钟无进展
- 稳定性风险：Master NotReady 可能导致控制面不可用
- 资源浪费：频繁 API 调用增加 API Server 负载

### Measurable Goals

1. 健康检查收敛时间从 7 分 14 秒降低到 3 分钟以内（提升 57%）
2. Master NotReady 次数从 3 次降低到 0 次
3. API 调用次数从约 100 次降低到约 30 次（降低 70%）
4. 关键组件失败检测时间从约 7 分钟降低到约 1 秒

### Non-Goals

1. 优化 Calico 本身的部署时间（由 KEP-10 处理）
2. 修改 Kubernetes 控制面组件的行为
3. 改变健康检查的业务逻辑（哪些组件需要检查）

## Proposal

### User Stories

**Story 1: 快速集群创建**
作为集群管理员，我希望集群创建过程中的健康检查能够快速收敛，以便在更短的时间内获得可用的集群。

*当前状态*：健康检查耗时 7 分 14 秒，期间 Master 节点反复 NotReady
*期望状态*：健康检查在 3 分钟内完成，无 Master NotReady

**Story 2: 稳定的控制面**
作为集群管理员，我希望在集群创建过程中控制面保持稳定，避免 Master 节点 NotReady。

*当前状态*：Calico 部署后 Master 节点反复 NotReady
*期望状态*：控制面始终 Ready，无 NotReady 事件

**Story 3: 可配置的健康检查**
作为集群管理员，我希望能够根据实际需求配置健康检查的组件清单和检查间隔。

*当前状态*：健康检查配置硬编码
*期望状态*：通过配置文件灵活定义检查组件和间隔

### Notes/Constraints

1. **向后兼容**：必须保持与现有健康检查逻辑的兼容性
2. **配置灵活**：支持通过配置文件自定义检查组件和间隔
3. **缓存一致性**：缓存数据需要在合理时间内刷新，避免使用过期数据
4. **错误处理**：关键组件失败必须立即返回，非关键组件失败可以记录警告

### Implementation Approach

#### 优化 1: 渐进式检查架构

**架构设计：**

```
┌─────────────────────────────────────────────────────────────┐
│                    统一健康检查架构                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  1. 初始化阶段                                               │
│     ├─ 初始化缓存                                           │
│     └─ 加载检查配置                                         │
│                                                             │
│  2. 渐进式检查阶段（按优先级分 4 个阶段）                      │
│     ├─ 阶段 1: 节点状态检查（并行）                          │
│     ├─ 阶段 2: 关键组件检查（并行）                          │
│     ├─ 阶段 3: 重要组件检查（并行）                          │
│     └─ 阶段 4: 非关键组件检查（并行）                        │
│                                                             │
│  3. 结果处理阶段                                             │
│     ├─ 聚合检查结果                                         │
│     ├─ 动态调整下次检查间隔                                  │
│     └─ 更新缓存                                             │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**核心设计原则：**

| 原则 | 说明 | 对应优化点 |
|------|------|-----------|
| **渐进式** | 按优先级分阶段检查，关键组件失败立即返回 | 渐进式检查 + 优先级检查 |
| **并行化** | 每个阶段内使用并行检查 | 并行检查 |
| **缓存化** | 使用缓存减少 API 调用 | 缓存机制 |
| **智能化** | 根据检查结果动态调整间隔 | 动态间隔 |

#### 优化 2: 统一健康检查器实现

**文件**: `pkg/kube/health.go`

```go
// HealthCheckConfig 健康检查配置
type HealthCheckConfig struct {
    // 缓存配置
    CacheTTL time.Duration `yaml:"cacheTTL"`
    
    // 检查间隔配置
    CriticalComponentInterval  time.Duration `yaml:"criticalComponentInterval"`  // 关键组件失败：5 秒
    ImportantComponentInterval time.Duration `yaml:"importantComponentInterval"` // 重要组件失败：15 秒
    OptionalComponentInterval  time.Duration `yaml:"optionalComponentInterval"`  // 非关键组件失败：30 秒
    NormalInterval             time.Duration `yaml:"normalInterval"`             // 正常：5 分钟
    
    // 组件清单配置
    CriticalComponents  []ComponentCheck `yaml:"criticalComponents"`
    ImportantComponents []ComponentCheck `yaml:"importantComponents"`
    OptionalComponents  []ComponentCheck `yaml:"optionalComponents"`
}

// HealthCheckResult 健康检查结果
type HealthCheckResult struct {
    NodeErrors                []error
    CriticalComponentErrors   []error
    ImportantComponentErrors  []error
    OptionalComponentErrors   []error
}

// UnifiedHealthChecker 统一健康检查器
type UnifiedHealthChecker struct {
    kubeClient kubernetes.Interface
    log        *log.Logger
    cache      *HealthCheckCache
    config     HealthCheckConfig
}

// NewUnifiedHealthChecker 创建健康检查器
func NewUnifiedHealthChecker(kubeClient kubernetes.Interface, log *log.Logger, config HealthCheckConfig) *UnifiedHealthChecker {
    return &UnifiedHealthChecker{
        kubeClient: kubeClient,
        log:        log,
        cache:      NewHealthCheckCache(config.CacheTTL),
        config:     config,
    }
}

// DefaultHealthCheckConfig 默认配置
func DefaultHealthCheckConfig() HealthCheckConfig {
    return HealthCheckConfig{
        CacheTTL:                   30 * time.Second,
        CriticalComponentInterval:  5 * time.Second,
        ImportantComponentInterval: 15 * time.Second,
        OptionalComponentInterval:  30 * time.Second,
        NormalInterval:             5 * time.Minute,
        
        CriticalComponents: []ComponentCheck{
            {Namespace: "kube-system", Prefixes: []string{"etcd-", "kube-apiserver-", "kube-controller-manager-", "kube-scheduler-"}},
        },
        ImportantComponents: []ComponentCheck{
            {Namespace: "kube-system", Prefixes: []string{"calico-kube-controllers", "calico-node", "coredns", "kube-proxy-"}},
        },
        OptionalComponents: []ComponentCheck{
            {Namespace: "kube-system", Prefixes: []string{"metrics-server-"}},
            {Namespace: "ingress-nginx", Prefixes: []string{"ingress-nginx-controller"}},
            {Namespace: "monitoring", Prefixes: []string{"alertmanager-main-", "prometheus-k8s-", "node-exporter-"}},
            {Namespace: "openfuyao-system", Prefixes: []string{"console-service-", "oauth-server-", "local-harbor-"}},
        },
    }
}

// LoadHealthCheckConfig 从配置文件加载配置
func LoadHealthCheckConfig(configPath string) HealthCheckConfig {
    data, err := os.ReadFile(configPath)
    if err != nil {
        log.Warnf("failed to load health check config from %s, using default: %v", configPath, err)
        return DefaultHealthCheckConfig()
    }
    
    var config HealthCheckConfig
    if err := yaml.Unmarshal(data, &config); err != nil {
        log.Warnf("failed to parse health check config, using default: %v", err)
        return DefaultHealthCheckConfig()
    }
    
    // 如果某些字段为空，使用默认值
    defaultConfig := DefaultHealthCheckConfig()
    if len(config.CriticalComponents) == 0 {
        config.CriticalComponents = defaultConfig.CriticalComponents
    }
    if len(config.ImportantComponents) == 0 {
        config.ImportantComponents = defaultConfig.ImportantComponents
    }
    if len(config.OptionalComponents) == 0 {
        config.OptionalComponents = defaultConfig.OptionalComponents
    }
    
    return config
}

// CheckClusterHealth 统一健康检查入口
func CheckClusterHealth(kubeClient kubernetes.Interface, log *log.Logger, cluster *bkev1beta1.BKECluster, currentVersion string, bkeNodes bkev1beta1.BKENodes) error {
    config := LoadHealthCheckConfig("/etc/bke/health-check-config.yaml")
    checker := NewUnifiedHealthChecker(kubeClient, log, config)
    return checker.Check(cluster, currentVersion, bkeNodes)
}

// Check 执行统一健康检查
func (h *UnifiedHealthChecker) Check(cluster *bkev1beta1.BKECluster, currentVersion string, bkeNodes bkev1beta1.BKENodes) error {
    result := &HealthCheckResult{}
    
    // 阶段 1: 节点状态检查（并行）
    if err := h.checkNodesParallel(cluster, currentVersion, bkeNodes, result); err != nil {
        result.NodeErrors = append(result.NodeErrors, err)
        return h.aggregateResult(result)
    }
    
    // 阶段 2: 关键组件检查（并行）
    if err := h.checkCriticalComponentsParallel(result); err != nil {
        result.CriticalComponentErrors = append(result.CriticalComponentErrors, err)
        return h.aggregateResult(result)
    }
    
    // 阶段 3: 重要组件检查（并行）
    if err := h.checkImportantComponentsParallel(result); err != nil {
        h.log.Warn("important components check failed: %v", err)
        result.ImportantComponentErrors = append(result.ImportantComponentErrors, err)
    }
    
    // 阶段 4: 非关键组件检查（并行）
    if err := h.checkOptionalComponentsParallel(result); err != nil {
        h.log.Debug("optional components check failed: %v", err)
        result.OptionalComponentErrors = append(result.OptionalComponentErrors, err)
    }
    
    return h.aggregateResult(result)
}

// checkNodesParallel 并行检查节点状态
func (h *UnifiedHealthChecker) checkNodesParallel(cluster *bkev1beta1.BKECluster, currentVersion string, bkeNodes bkev1beta1.BKENodes, result *HealthCheckResult) error {
    // 从缓存获取节点列表
    nodes, err := h.cache.GetNodes(h.kubeClient)
    if err != nil {
        return err
    }
    
    // 并行检查所有节点
    var wg sync.WaitGroup
    errChan := make(chan error, len(nodes.Items))
    
    for _, node := range nodes.Items {
        nodeIP := GetNodeIP(&node)
        
        // 跳过需要跳过的节点
        if bkeNodes.GetNodeStateNeedSkip(nodeIP) {
            continue
        }
        
        wg.Add(1)
        go func(n corev1.Node) {
            defer wg.Done()
            if err := h.checkNode(&n, currentVersion); err != nil {
                errChan <- err
            }
        }(node)
    }
    
    wg.Wait()
    close(errChan)
    
    // 收集错误
    for err := range errChan {
        result.NodeErrors = append(result.NodeErrors, err)
    }
    
    if len(result.NodeErrors) > 0 {
        return kerrors.NewAggregate(result.NodeErrors)
    }
    
    return nil
}

// checkCriticalComponentsParallel 并行检查关键组件
func (h *UnifiedHealthChecker) checkCriticalComponentsParallel(result *HealthCheckResult) error {
    return h.checkComponentsByPriority(h.config.CriticalComponents, result)
}

// checkImportantComponentsParallel 并行检查重要组件
func (h *UnifiedHealthChecker) checkImportantComponentsParallel(result *HealthCheckResult) error {
    return h.checkComponentsByPriority(h.config.ImportantComponents, result)
}

// checkOptionalComponentsParallel 并行检查非关键组件
func (h *UnifiedHealthChecker) checkOptionalComponentsParallel(result *HealthCheckResult) error {
    return h.checkComponentsByPriority(h.config.OptionalComponents, result)
}

// checkComponentsByPriority 按优先级并行检查组件
func (h *UnifiedHealthChecker) checkComponentsByPriority(components []ComponentCheck, result *HealthCheckResult) error {
    var wg sync.WaitGroup
    errChan := make(chan error, len(components))
    
    for _, check := range components {
        wg.Add(1)
        go func(c ComponentCheck) {
            defer wg.Done()
            if err := h.checkComponent(c); err != nil {
                errChan <- err
            }
        }(check)
    }
    
    wg.Wait()
    close(errChan)
    
    var errs []error
    for err := range errChan {
        errs = append(errs, err)
    }
    
    if len(errs) > 0 {
        return kerrors.NewAggregate(errs)
    }
    
    return nil
}

// aggregateResult 聚合检查结果
func (h *UnifiedHealthChecker) aggregateResult(result *HealthCheckResult) error {
    // 节点错误或关键组件错误，立即返回
    if len(result.NodeErrors) > 0 || len(result.CriticalComponentErrors) > 0 {
        var allErrors []error
        allErrors = append(allErrors, result.NodeErrors...)
        allErrors = append(allErrors, result.CriticalComponentErrors...)
        return kerrors.NewAggregate(allErrors)
    }
    
    // 重要组件错误，记录警告
    if len(result.ImportantComponentErrors) > 0 {
        h.log.Warn("important component errors: %v", result.ImportantComponentErrors)
    }
    
    // 非关键组件错误，记录调试信息
    if len(result.OptionalComponentErrors) > 0 {
        h.log.Debug("optional component errors: %v", result.OptionalComponentErrors)
    }
    
    h.log.Info("cluster health check pass")
    return nil
}

// GetRequeueInterval 根据检查结果动态调整间隔
func GetRequeueInterval(result *HealthCheckResult, config HealthCheckConfig) time.Duration {
    // 节点错误或关键组件错误，快速重试
    if len(result.NodeErrors) > 0 || len(result.CriticalComponentErrors) > 0 {
        return config.CriticalComponentInterval
    }
    
    // 重要组件错误，中速重试
    if len(result.ImportantComponentErrors) > 0 {
        return config.ImportantComponentInterval
    }
    
    // 非关键组件错误，慢速重试
    if len(result.OptionalComponentErrors) > 0 {
        return config.OptionalComponentInterval
    }
    
    // 正常，使用正常间隔
    return config.NormalInterval
}
```

#### 优化 3: 健康检查缓存实现

**文件**: `pkg/kube/health_cache.go`

```go
// HealthCheckCache 健康检查缓存
type HealthCheckCache struct {
    pods     map[string][]corev1.Pod
    nodes    *corev1.NodeList
    lastSync time.Time
    ttl      time.Duration
    mu       sync.RWMutex
}

// NewHealthCheckCache 创建缓存
func NewHealthCheckCache(ttl time.Duration) *HealthCheckCache {
    return &HealthCheckCache{
        pods: make(map[string][]corev1.Pod),
        ttl:  ttl,
    }
}

// GetNodes 从缓存获取节点列表
func (c *HealthCheckCache) GetNodes(client *Client) (*corev1.NodeList, error) {
    c.mu.RLock()
    if c.nodes != nil && time.Since(c.lastSync) < c.ttl {
        defer c.mu.RUnlock()
        return c.nodes, nil
    }
    c.mu.RUnlock()
    
    // 缓存过期，重新获取
    c.mu.Lock()
    defer c.mu.Unlock()
    
    nodes, err := client.ListNodes(nil)
    if err != nil {
        return nil, err
    }
    
    c.nodes = nodes
    c.lastSync = time.Now()
    
    return nodes, nil
}

// GetPods 从缓存获取 Pod 列表
func (c *HealthCheckCache) GetPods(client *Client, namespace string) ([]corev1.Pod, error) {
    c.mu.RLock()
    if pods, ok := c.pods[namespace]; ok && time.Since(c.lastSync) < c.ttl {
        defer c.mu.RUnlock()
        return pods, nil
    }
    c.mu.RUnlock()
    
    // 缓存过期，重新获取
    c.mu.Lock()
    defer c.mu.Unlock()
    
    pods, err := client.getPods(namespace)
    if err != nil {
        return nil, err
    }
    
    c.pods[namespace] = pods
    c.lastSync = time.Now()
    
    return pods, nil
}
```

#### 优化 4: 配置文件

**文件**: `/etc/bke/health-check-config.yaml`

```yaml
cacheTTL: 30s
criticalComponentInterval: 5s
importantComponentInterval: 15s
optionalComponentInterval: 30s
normalInterval: 5m

criticalComponents:
  - namespace: kube-system
    prefixes:
      - etcd-
      - kube-apiserver-
      - kube-controller-manager-
      - kube-scheduler-

importantComponents:
  - namespace: kube-system
    prefixes:
      - calico-kube-controllers
      - calico-node
      - coredns
      - kube-proxy-

optionalComponents:
  - namespace: kube-system
    prefixes:
      - metrics-server-
  - namespace: ingress-nginx
    prefixes:
      - ingress-nginx-controller
  - namespace: monitoring
    prefixes:
      - alertmanager-main-
      - prometheus-k8s-
      - node-exporter-
  - namespace: openfuyao-system
    prefixes:
      - console-service-
      - oauth-server-
      - local-harbor-
```

**配置说明：**

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `cacheTTL` | 缓存过期时间 | 30s |
| `criticalComponentInterval` | 关键组件失败后的重试间隔 | 5s |
| `importantComponentInterval` | 重要组件失败后的重试间隔 | 15s |
| `optionalComponentInterval` | 非关键组件失败后的重试间隔 | 30s |
| `normalInterval` | 正常状态下的检查间隔 | 5m |
| `criticalComponents` | 关键组件清单 | etcd, kube-apiserver 等 |
| `importantComponents` | 重要组件清单 | calico, coredns 等 |
| `optionalComponents` | 非关键组件清单 | metrics-server, ingress-nginx 等 |

**配置加载优先级：**

1. 如果配置文件存在且格式正确，使用配置文件
2. 如果配置文件不存在或格式错误，使用默认配置
3. 如果配置文件中某些字段为空，使用默认值填充

### Test Plan

#### 单元测试

**文件**: `pkg/kube/health_test.go`

```go
func TestUnifiedHealthCheck(t *testing.T) {
    // 部署 64 节点集群
    cluster := createTestCluster(64)
    
    // 记录健康检查时间
    start := time.Now()
    
    // 执行统一健康检查
    err := cluster.CheckClusterHealth()
    require.NoError(t, err)
    
    elapsed := time.Since(start)
    
    // 验证检查时间
    assert.Less(t, elapsed, 3*time.Minute, "Health check should complete within 3 minutes")
    t.Logf("Health check completed in %v", elapsed)
}

func TestCriticalComponentFastFail(t *testing.T) {
    // 模拟关键组件失败场景
    cluster := createTestClusterWithFailedComponent("etcd-master-1")
    
    start := time.Now()
    
    // 执行健康检查
    err := cluster.CheckClusterHealth()
    require.Error(t, err)
    
    elapsed := time.Since(start)
    
    // 验证快速失败（应该在 1 秒内返回）
    assert.Less(t, elapsed, 1*time.Second, "Critical component failure should fail fast")
    t.Logf("Fast fail completed in %v", elapsed)
}

func TestDynamicRequeueInterval(t *testing.T) {
    tests := []struct {
        name     string
        result   *HealthCheckResult
        expected time.Duration
    }{
        {
            name: "critical component error",
            result: &HealthCheckResult{
                CriticalComponentErrors: []error{errors.New("etcd failed")},
            },
            expected: 5 * time.Second,
        },
        {
            name: "important component error",
            result: &HealthCheckResult{
                ImportantComponentErrors: []error{errors.New("calico failed")},
            },
            expected: 15 * time.Second,
        },
        {
            name: "optional component error",
            result: &HealthCheckResult{
                OptionalComponentErrors: []error{errors.New("metrics-server failed")},
            },
            expected: 30 * time.Second,
        },
        {
            name:     "no error",
            result:   &HealthCheckResult{},
            expected: 5 * time.Minute,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            interval := GetRequeueInterval(tt.result, DefaultHealthCheckConfig())
            assert.Equal(t, tt.expected, interval)
        })
    }
}
```

#### 集成测试

**文件**: `test/integration/health_check_test.go`

```go
func TestHealthCheckPerformance(t *testing.T) {
    // 创建 64 节点集群
    cluster := createTestCluster(64)
    
    // 记录健康检查时间
    start := time.Now()
    
    // 执行健康检查
    err := cluster.CheckClusterHealth()
    require.NoError(t, err)
    
    elapsed := time.Since(start)
    
    // 验证性能
    assert.Less(t, elapsed, 3*time.Minute, "Health check should complete within 3 minutes")
    t.Logf("Health check completed in %v", elapsed)
    
    // 验证 API 调用次数
    apiCalls := cluster.GetAPICallCount()
    assert.Less(t, apiCalls, 30, "API calls should be less than 30")
    t.Logf("API calls: %d", apiCalls)
}
```

#### 端到端测试

```bash
# 创建 64 节点集群
kubectl apply -f bkecluster-64n.yaml

# 监控集群状态
watch -n 5 'kubectl get bkecluster bke-cluster-128n -o jsonpath="{.status.clusterStatus}"'

# 期望: ClusterUnhealthy → ClusterReady 时间 < 3 分钟

# 检查 Master 节点状态
kubectl get nodes -l node-role.kubernetes.io/master

# 期望: 所有 Master 节点 Ready，无 NotReady 事件

# 检查健康检查日志
kubectl logs -n bke-system deployment/bke-controller-manager | grep "health check"

# 期望: 健康检查通过，无频繁重试
```

### Graduation Criteria

#### Alpha (v0.1)
- [ ] 实现统一健康检查器
- [ ] 实现缓存机制
- [ ] 实现动态间隔
- [ ] 单元测试通过

#### Beta (v0.2)
- [ ] 集成测试通过
- [ ] 健康检查收敛时间 < 5 分钟
- [ ] API 调用次数 < 50 次
- [ ] 配置文件支持

#### Stable (v1.0)
- [ ] 端到端测试通过
- [ ] 健康检查收敛时间 < 3 分钟
- [ ] Master NotReady 次数 = 0
- [ ] 生产环境运行 1 个月无问题

## Design Details

### API Changes

本 KEP 不引入新的 API 变更。所有变更都是内部实现优化。

### Code Changes

#### 1. 统一健康检查器

**文件**: `pkg/kube/health.go`

- 新增 `HealthCheckConfig` 结构体
- 新增 `HealthCheckResult` 结构体
- 新增 `UnifiedHealthChecker` 结构体
- 重构 `CheckClusterHealth()` 函数
- 新增 `checkNodesParallel()` 函数
- 新增 `checkCriticalComponentsParallel()` 函数
- 新增 `checkImportantComponentsParallel()` 函数
- 新增 `checkOptionalComponentsParallel()` 函数
- 新增 `checkComponentsByPriority()` 函数
- 新增 `aggregateResult()` 函数
- 新增 `GetRequeueInterval()` 函数

#### 2. 健康检查缓存

**文件**: `pkg/kube/health_cache.go` (新文件)

- 新增 `HealthCheckCache` 结构体
- 新增 `NewHealthCheckCache()` 函数
- 新增 `GetNodes()` 方法
- 新增 `GetPods()` 方法

#### 3. 配置加载

**文件**: `pkg/kube/health.go`

- 新增 `LoadHealthCheckConfig()` 函数
- 新增 `DefaultHealthCheckConfig()` 函数

#### 4. 动态间隔

**文件**: `pkg/phaseframe/phases/ensure_cluster.go`

- 修改 `runHealthChecks()` 函数，使用 `GetRequeueInterval()` 动态调整间隔

### Upgrade / Downgrade Strategy

**升级策略：**
- 配置文件 `/etc/bke/health-check-config.yaml` 可选，不存在时使用默认配置
- 新代码完全兼容旧的健康检查逻辑
- 可以渐进式部署，先部署到部分节点验证

**降级策略：**
- 删除配置文件即可回退到默认配置
- 代码回退简单，只需恢复原有的 `CheckClusterHealth()` 实现

## Drawbacks

1. **复杂度增加**：引入了缓存、配置、动态间隔等机制，代码复杂度增加
   - 缓解：通过良好的代码组织和文档降低维护成本

2. **缓存一致性**：缓存可能导致使用过期数据
   - 缓解：设置合理的缓存 TTL（默认 30 秒），在关键检查时强制刷新

3. **配置错误风险**：配置文件格式错误可能导致健康检查失败
   - 缓解：配置文件加载失败时使用默认配置，记录警告日志

## Alternatives

### Alternative 1: 仅优化 Master NotReady 问题

**方案**：只修复 Calico 部署导致的 Master NotReady 问题，不改变健康检查机制

**优点**：
- 改动小，风险低
- 直接解决根本问题

**缺点**：
- 不解决健康检查机制本身的问题
- 无法优化 API 调用次数
- 无法动态调整检查间隔

**决策**：拒绝。需要同时优化健康检查机制和 Master NotReady 问题。

### Alternative 2: 仅优化健康检查机制

**方案**：只优化健康检查机制（并行化、缓存、动态间隔），不修复 Master NotReady

**优点**：
- 减少 API 调用次数
- 提高检查效率

**缺点**：
- Master NotReady 仍然存在
- 健康检查仍然会失败

**决策**：拒绝。需要同时优化健康检查机制和 Master NotReady 问题。

### Alternative 3: 使用 Kubernetes 原生健康检查

**方案**：使用 Kubernetes 原生的 Readiness Probe 和 Liveness Probe，不实现自定义健康检查

**优点**：
- 使用 Kubernetes 原生机制
- 减少自定义代码

**缺点**：
- 无法实现渐进式检查
- 无法动态调整检查间隔
- 无法缓存检查结果

**决策**：拒绝。自定义健康检查提供更细粒度的控制和优化空间。

## Infrastructure Needed

1. **测试环境**：64 节点集群用于端到端测试
2. **监控工具**：Prometheus + Grafana 用于监控健康检查性能
3. **日志系统**：ELK 或 Loki 用于分析健康检查日志

## Implementation History

- **2026-07-13**: 识别健康检查收敛慢问题
- **2026-07-14**: 分析根因，确认 Master NotReady 与 Calico 部署的因果关系
- **2026-07-15**: 设计统一健康检查架构
- **TBD**: Alpha 实现
- **TBD**: Beta 实现
- **TBD**: Stable 发布

## References

1. [BKE 64 节点集群性能瓶颈分析与优化方案](../../performance/report/64节点集群性能瓶颈分析与优化方案.md)
2. [KEP-10: 消除 API Throttling](../kep10/kep10-api-throttling-optimization.md)
3. [Kubernetes Health Checking](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/)
4. [Controller Runtime Health Checks](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/healthz)

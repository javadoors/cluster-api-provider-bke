# KEP-12: 优化 Calico 部署性能

<!--
这是 KEP 模板的说明，实际文档中应删除此注释块
 KEP 编号: 12
 标题: 优化 Calico 部署性能
 状态: Draft
 创建日期: 2026-07-15
 最后更新: 2026-07-15
 作者: BKE Team
 审阅者: TBD
 批准者: TBD
-->

## 摘要

本 KEP 旨在优化 BKE 平台中 Calico 网络插件的部署性能，将 64 节点集群的 Calico 部署时间从当前的 3 分 15 秒降低到 1 分 35 秒，提升约 51%。

通过五项关键优化措施：
1. **镜像预置**：在节点环境初始化阶段提前拉取 Calico 镜像，节省约 50 秒
2. **并行部署**：调整 DaemonSet 的 maxUnavailable 参数为 30%，节省约 20 秒
3. **网络模式优化**：使用 VXLAN 模式替代 IPIP 模式，节省约 30 秒
4. **控制面优化**：优化 calico-kube-controllers 启动参数，节省约 20 秒
5. **配置精简**：禁用不必要的功能，节省约 10 秒

## 动机

### 为什么需要这个 KEP？

在 64 节点集群的性能测试中，Calico 部署耗时 3 分 15 秒，占 Addon 部署阶段的 78.6%，是第三大性能瓶颈。虽然优先级低于 API Throttling（P0）和健康检查收敛慢（P1），但 Calico 部署优化仍有显著价值：

1. **用户体验**：减少集群创建总时间，提升用户满意度
2. **资源效率**：减少镜像拉取时的网络带宽占用
3. **可预测性**：通过镜像预置提高部署时间的可预测性
4. **可扩展性**：为更大规模集群（128+ 节点）的部署优化奠定基础

### 当前问题分析

#### 性能数据

| 指标 | 当前值 | 目标值 | 提升幅度 |
|------|--------|--------|----------|
| Calico 部署总时间 | 3 分 15 秒 | 1 分 35 秒 | 51% |
| 镜像拉取时间 | ~1 分钟 | ~10 秒 | 83% |
| 网络初始化时间 | ~1.5 分钟 | ~30 秒 | 50% |
| 控制面注册时间 | ~30 秒 | ~10 秒 | 67% |
| 配置加载时间 | ~15 秒 | ~5 秒 | 67% |

#### 根因分析

**1. 镜像拉取瓶颈（~1 分钟）**

- **问题**：64 个节点同时拉取 Calico 镜像，造成 Registry 带宽压力
- **镜像清单**：calico-node、calico-cni、calico-kube-controllers、calico-pod2daemon-flexvol
- **总拉取量**：64 节点 × 4 镜像 × ~100MB/镜像 = ~25.6GB
- **带宽计算**：假设 Registry 带宽 1Gbps，理论拉取时间 = 25.6GB × 8 / 1Gbps = ~200 秒

**2. 网络初始化瓶颈（~1.5 分钟）**

- **问题**：IPIP 模式需要为每对节点建立隧道，64 节点需要建立 64 × 63 / 2 = 2016 个 IPIP 隧道
- **开销**：每个数据包增加 20 字节 IP 头封装
- **BGP 会话**：64 节点 × 3 Master = 192 个 BGP 会话

**3. 控制面注册瓶颈（~30 秒）**

- **问题**：calico-kube-controllers 默认启用 5 个控制器（node、policy、namespace、serviceaccount、endpoint）
- **初始化时间**：5 控制器 × ~6 秒/控制器 = ~30 秒

**4. DaemonSet 串行部署**

- **问题**：默认 maxUnavailable=1，64 个节点需要 64 轮更新
- **每轮时间**：~1.5 秒
- **总时间**：64 × 1.5 = ~96 秒

**5. 配置加载开销（~15 秒）**

- **问题**：Calico 默认启用 Prometheus 指标、启动清理、健康检查等功能
- **每个功能初始化时间**：~3 秒
- **总时间**：5 功能 × 3 秒 = ~15 秒

## 目标

### 主要目标

1. **减少 Calico 部署时间**：从 3 分 15 秒降低到 1 分 35 秒（提升 51%）
2. **提高部署可预测性**：通过镜像预置减少网络延迟对部署时间的影响
3. **降低资源消耗**：减少镜像拉取时的网络带宽占用和 CPU 使用

### 次要目标

1. **提升网络性能**：VXLAN 模式相比 IPIP 模式减少封装开销
2. **简化运维**：减少不必要的功能，降低配置复杂度
3. **增强可扩展性**：为 128+ 节点集群的部署优化奠定基础

### 非目标

1. **不改变 Calico 的核心功能**：保持网络策略、路由等核心功能不变
2. **不升级 Calico 版本**：在当前 v3.31.3 版本基础上进行优化
3. **不修改 BKE 架构**：仅优化 Calico 部署流程，不改变 BKE 整体架构

## 提案

### 优化方案总览

| 优化项 | 原理 | 预期效果 | 实施难度 | 风险 |
|--------|------|----------|----------|------|
| **镜像预置** | 在节点环境初始化阶段提前拉取镜像 | 节省 ~50 秒 | 低 | 低 |
| **并行部署** | 调整 DaemonSet 的 maxUnavailable 参数 | 节省 ~20 秒 | 中 | 中 |
| **网络模式优化** | 使用 VXLAN 模式替代 IPIP 模式 | 节省 ~30 秒 | 中 | 中 |
| **控制面优化** | 优化 calico-kube-controllers 启动参数 | 节省 ~20 秒 | 中 | 中 |
| **配置精简** | 禁用不必要的功能 | 节省 ~10 秒 | 低 | 低 |

### 详细设计

#### 1. 镜像预置（节省 ~50 秒）

**原理**：在节点环境初始化阶段（EnsureNodesEnv）提前拉取 Calico 镜像，此时集群尚未创建，网络压力较小，可以充分利用带宽。

**实现方案**：

```go
// pkg/phaseframe/phases/ensure_nodes_env.go

// CalicoImages 定义需要预置的 Calico 镜像
var CalicoImages = []string{
    "registry.openfuyao.com/calico/node:v3.31.3",
    "registry.openfuyao.com/calico/cni:v3.31.3",
    "registry.openfuyao.com/calico/kube-controllers:v3.31.3",
    "registry.openfuyao.com/calico/pod2daemon-flexvol:v3.31.3",
}

// prePullCalicoImages 预置 Calico 镜像
func (e *EnsureNodesEnv) prePullCalicoImages(nodes bkenode.Nodes) error {
    var wg sync.WaitGroup
    errChan := make(chan error, len(nodes)*len(CalicoImages))
    
    // 并行预置：每个节点一个 goroutine
    for _, node := range nodes {
        wg.Add(1)
        go func(n bkenode.Node) {
            defer wg.Done()
            
            // 每个节点并行预置所有镜像
            var nodeWg sync.WaitGroup
            for _, image := range CalicoImages {
                nodeWg.Add(1)
                go func(img string) {
                    defer nodeWg.Done()
                    
                    // 检查镜像是否已存在
                    if e.imageExists(n, img) {
                        e.Ctx.Log.Debug("Calico image %s already exists on node %s", img, n.IP)
                        return
                    }
                    
                    // 预置镜像
                    cmd := fmt.Sprintf("crictl pull %s", img)
                    if err := e.executeOnNode(n, cmd); err != nil {
                        errChan <- fmt.Errorf("failed to pre-pull image %s on node %s: %v", img, n.IP, err)
                    } else {
                        e.Ctx.Log.Info("Successfully pre-pulled Calico image %s on node %s", img, n.IP)
                    }
                }(image)
            }
            nodeWg.Wait()
        }(node)
    }
    
    wg.Wait()
    close(errChan)
    
    // 收集错误
    var errs []error
    for err := range errChan {
        errs = append(errs, err)
    }
    
    if len(errs) > 0 {
        e.Ctx.Log.Warn("Some Calico images failed to pre-pull, will fallback to normal pull: %v", errs)
    }
    
    return nil
}

// imageExists 检查镜像是否已存在
func (e *EnsureNodesEnv) imageExists(node bkenode.Node, image string) bool {
    cmd := fmt.Sprintf("crictl images | grep -q '%s'", image)
    return e.executeOnNode(node, cmd) == nil
}
```

**预期效果**：

| 指标 | 优化前 | 优化后 | 提升 |
|------|--------|--------|------|
| 镜像拉取时间 | ~1 分钟 | ~10 秒 | 83% |
| 节省时间 | - | ~50 秒 | - |

#### 2. 并行部署（节省 ~20 秒）

**原理**：调整 DaemonSet 的 maxUnavailable 参数为 30%，允许 30% 的节点（~19 个节点）同时更新，减少更新轮数。

**实现方案**：

```yaml
# bke-manifests/kubernetes/calico/v3.31.3/calico.yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: calico-node
  namespace: kube-system
spec:
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 30%  # 允许 30% 的节点同时更新
      maxSurge: 0          # 不允许额外创建 Pod
  template:
    spec:
      containers:
      - name: calico-node
        # 启用 readinessProbe，加速 Pod 就绪
        readinessProbe:
          httpGet:
            path: /readiness
            port: 9080
          initialDelaySeconds: 0
          periodSeconds: 1
          timeoutSeconds: 1
          successThreshold: 1
          failureThreshold: 3
```

**readinessProbe 加速原理**：

| 参数 | 默认值 | 优化值 | 说明 |
|------|--------|--------|------|
| `initialDelaySeconds` | 0 | 0 | 容器启动后立即开始检查 |
| `periodSeconds` | 10 | **1** | 检查频率从 10 秒提高到 1 秒 |
| `timeoutSeconds` | 1 | 1 | 超时时间保持 1 秒 |
| `successThreshold` | 1 | 1 | 连续 1 次成功即就绪 |
| `failureThreshold` | 3 | 3 | 连续 3 次失败才认为未就绪 |

**时间节省分析**：

| 场景 | 默认配置 | 优化配置 | 节省时间 |
|------|---------|---------|---------|
| 单个 Pod 就绪检测 | ~5 秒 | ~0.5 秒 | ~4.5 秒 |
| 64 节点串行部署 | ~320 秒 | ~32 秒 | ~288 秒 |
| 64 节点并行部署（30%） | ~96 秒 | ~10 秒 | ~86 秒 |
| **实际节省** | - | - | **~20 秒** |

**预期效果**：

| 指标 | 优化前 | 优化后 | 提升 |
|------|--------|--------|------|
| 网络初始化时间 | ~1.5 分钟 | ~1 分钟 | 33% |
| 节省时间 | - | ~20 秒 | - |

#### 3. 网络模式优化（节省 ~30 秒）

**原理**：使用 VXLAN 模式替代 IPIP 模式。VXLAN 使用 UDP 封装，开销更小（8 字节 VXLAN 头 + 8 字节 UDP 头），且不需要为每对节点建立隧道。

**实现方案**：

```yaml
# bke-manifests/kubernetes/calico/v3.31.3/calico.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: calico-config
  namespace: kube-system
data:
  # 使用 VXLAN 模式（比 IPIP 更快）
  calico_backend: "vxlan"
  
  # 禁用 BGP（如果不需要）
  bird_ready: "false"
  
  # 优化网络参数
  vxlan_vni: "4096"
  vxlan_port: "4789"
  
  # 禁用 IPv6（如果不需要）
  ipv6_support: "false"
  
  # 优化日志级别
  log_level: "warning"
```

**VXLAN vs IPIP 对比**：

| 特性 | IPIP | VXLAN |
|------|------|-------|
| 封装开销 | 20 字节 IP 头 | 8 字节 VXLAN + 8 字节 UDP |
| 隧道数量 | N × (N-1) / 2 | 1 个 VXLAN 网络 |
| BGP 会话 | 需要 | 不需要 |
| 网络性能 | 较低 | 较高 |
| 兼容性 | 广泛支持 | 需要内核 3.10+ |

**预期效果**：

| 指标 | 优化前 | 优化后 | 提升 |
|------|--------|--------|------|
| 网络初始化时间 | ~1 分钟 | ~30 秒 | 50% |
| 节省时间 | - | ~30 秒 | - |

#### 4. 控制面优化（节省 ~20 秒）

**原理**：只启用必要的控制器（node 控制器），禁用不必要的控制器（policy、namespace、serviceaccount、endpoint）。

**实现方案**：

```yaml
# bke-manifests/kubernetes/calico/v3.31.3/calico.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: calico-kube-controllers
  namespace: kube-system
spec:
  template:
    spec:
      containers:
      - name: calico-kube-controllers
        env:
        # 减少初始同步的数据量
        - name: ENABLED_CONTROLLERS
          value: "node"  # 只启用 node 控制器，禁用其他控制器
        
        # 优化同步间隔
        - name: NODE_SYNC_PERIOD
          value: "30s"  # 从默认 5 分钟减少到 30 秒
        
        # 启用缓存
        - name: CACHE_ENABLED
          value: "true"
        
        # 优化日志级别
        - name: LOG_LEVEL
          value: "warning"
        
        # 设置资源限制
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            cpu: 200m
            memory: 256Mi
```

**控制器功能说明**：

| 控制器 | 功能 | 是否必需 |
|--------|------|----------|
| node | 节点生命周期管理 | ✅ 必需 |
| policy | 网络策略管理 | ❌ 可选 |
| namespace | 命名空间管理 | ❌ 可选 |
| serviceaccount | 服务账号管理 | ❌ 可选 |
| endpoint | 端点管理 | ❌ 可选 |

**预期效果**：

| 指标 | 优化前 | 优化后 | 提升 |
|------|--------|--------|------|
| 控制面注册时间 | ~30 秒 | ~10 秒 | 67% |
| 节省时间 | - | ~20 秒 | - |

#### 5. 配置精简（节省 ~10 秒）

**原理**：禁用不必要的功能，减少初始化开销。

**实现方案**：

```yaml
# bke-manifests/kubernetes/calico/v3.31.3/calico.yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: calico-node
  namespace: kube-system
spec:
  template:
    spec:
      containers:
      - name: calico-node
        env:
        # 禁用不必要的功能
        - name: FELIX_HEALTHENABLED
          value: "true"
        
        # 优化 Felix 配置
        - name: FELIX_CHAININSERTMODE
          value: "append"  # 使用 append 模式，减少 iptables 操作
        
        - name: FELIX_IPTABLESREFRESHINTERVAL
          value: "60"  # 从默认 90 秒减少到 60 秒
        
        - name: FELIX_PROMETHEUSMETRICSENABLED
          value: "false"  # 禁用 Prometheus 指标（减少初始化开销）
        
        # 优化初始化
        - name: FELIX_STARTUPCLEANUP
          value: "false"  # 禁用启动清理（减少初始化时间）
```

**功能禁用说明**：

| 功能 | 默认值 | 优化值 | 影响 |
|------|--------|--------|------|
| Prometheus 指标 | true | false | 可通过其他方式监控 |
| 启动清理 | true | false | 不影响正常运行 |
| iptables 刷新间隔 | 90s | 60s | 提高响应速度 |
| 链插入模式 | insert | append | 减少 iptables 操作 |

**预期效果**：

| 指标 | 优化前 | 优化后 | 提升 |
|------|--------|--------|------|
| 配置优化时间 | ~15 秒 | ~5 秒 | 67% |
| 节省时间 | - | ~10 秒 | - |

### 综合优化效果

| 优化项 | 当前耗时 | 优化后耗时 | 节省时间 | 提升 |
|--------|----------|-----------|----------|------|
| 镜像预置 | ~1 分钟 | ~10 秒 | ~50 秒 | 83% |
| 并行部署 | ~1.5 分钟 | ~1 分钟 | ~20 秒 | 33% |
| 网络初始化 | ~1 分钟 | ~30 秒 | ~30 秒 | 50% |
| 控制面注册 | ~30 秒 | ~10 秒 | ~20 秒 | 67% |
| 配置优化 | ~15 秒 | ~5 秒 | ~10 秒 | 67% |
| **总计** | **3 分 15 秒** | **~1 分 35 秒** | **~1 分 40 秒** | **51%** |

### 实施步骤

| 阶段 | 任务 | 预计时间 | 风险 |
|------|------|---------|------|
| **阶段 1** | 镜像预置 | 1 天 | 低 |
| **阶段 2** | 并行部署优化 | 1 天 | 中 |
| **阶段 3** | 网络初始化优化 | 1 天 | 中 |
| **阶段 4** | 控制面注册优化 | 0.5 天 | 中 |
| **阶段 5** | 配置优化 | 0.5 天 | 低 |
| **总计** | | **4 天** | |

### 监控和验证

#### 监控指标

```go
// pkg/metrics/calico_deployment.go

type CalicoDeploymentMetrics struct {
    ImagePullDuration       time.Duration  // 镜像拉取时间
    NetworkInitDuration     time.Duration  // 网络初始化时间
    ControlPlaneRegDuration time.Duration  // 控制面注册时间
    TotalDuration           time.Duration  // 总部署时间
    FailedNodes             int            // 失败节点数
    SuccessNodes            int            // 成功节点数
}

// RecordCalicoDeployment 记录 Calico 部署指标
func RecordCalicoDeployment(metrics CalicoDeploymentMetrics) {
    calicoImagePullDuration.WithLabelValues().Observe(metrics.ImagePullDuration.Seconds())
    calicoNetworkInitDuration.WithLabelValues().Observe(metrics.NetworkInitDuration.Seconds())
    calicoControlPlaneRegDuration.WithLabelValues().Observe(metrics.ControlPlaneRegDuration.Seconds())
    calicoTotalDuration.WithLabelValues().Observe(metrics.TotalDuration.Seconds())
    calicoFailedNodes.WithLabelValues().Add(float64(metrics.FailedNodes))
    calicoSuccessNodes.WithLabelValues().Add(float64(metrics.SuccessNodes))
}
```

#### 验证测试

```go
// test/e2e/calico_deployment_test.go

func TestCalicoDeploymentPerformance(t *testing.T) {
    // 部署 64 节点集群
    cluster := createTestCluster(64)
    
    // 记录部署时间
    start := time.Now()
    
    // 部署 Calico Addon
    err := deployCalicoAddon(cluster)
    require.NoError(t, err)
    
    elapsed := time.Since(start)
    
    // 验证部署时间
    assert.Less(t, elapsed, 2*time.Minute, "Calico should deploy within 2 minutes")
    
    // 验证所有节点就绪
    nodes := getCalicoNodes(cluster)
    for _, node := range nodes {
        assert.True(t, node.Status.Conditions[0].Status == "True", 
            "Calico node %s should be ready", node.Name)
    }
    
    t.Logf("Calico deployed in %v", elapsed)
}
```

## 设计详情

### 代码变更清单

| 文件路径 | 变更类型 | 说明 |
|---------|---------|------|
| `pkg/phaseframe/phases/ensure_nodes_env.go` | 修改 | 添加 Calico 镜像预置逻辑 |
| `bke-manifests/kubernetes/calico/v3.31.3/calico.yaml` | 修改 | 调整 DaemonSet 配置、网络模式、控制器参数 |
| `pkg/metrics/calico_deployment.go` | 新增 | 添加 Calico 部署监控指标 |
| `test/e2e/calico_deployment_test.go` | 新增 | 添加 Calico 部署性能测试 |

### API 变更

本 KEP 不涉及 API 变更。

### 配置变更

| 配置项 | 旧值 | 新值 | 说明 |
|--------|------|------|------|
| `calico_backend` | `bird` | `vxlan` | 使用 VXLAN 模式 |
| `maxUnavailable` | `1` | `30%` | 允许 30% 节点并行更新 |
| `ENABLED_CONTROLLERS` | `node,policy,namespace,serviceaccount,endpoint` | `node` | 只启用 node 控制器 |
| `FELIX_PROMETHEUSMETRICSENABLED` | `true` | `false` | 禁用 Prometheus 指标 |
| `FELIX_STARTUPCLEANUP` | `true` | `false` | 禁用启动清理 |

### 向后兼容性

所有变更均向后兼容：
- 镜像预置失败时自动降级到正常拉取
- VXLAN 模式可通过配置回退到 IPIP 模式
- 控制器禁用不影响核心网络功能

## 升级与回滚策略

### 升级策略

1. **灰度发布**：先在测试环境验证，再推广到生产环境
2. **分阶段实施**：按优化项逐步实施，每阶段验证效果
3. **监控指标**：部署后持续监控 Calico 部署时间和成功率

### 回滚策略

如果优化后出现问题，可快速回滚：

1. **镜像预置回滚**：删除预置逻辑，恢复原有镜像拉取流程
2. **并行部署回滚**：将 `maxUnavailable` 改回 `1`
3. **网络模式回滚**：将 `calico_backend` 改回 `bird`
4. **控制器回滚**：将 `ENABLED_CONTROLLERS` 改回原有值

回滚操作可在 5 分钟内完成，不影响正在运行的集群。

## 测试计划

### 单元测试

- 镜像预置逻辑测试
- 镜像存在性检查测试
- 错误处理测试

### 集成测试

- 10 节点集群 Calico 部署测试
- 32 节点集群 Calico 部署测试
- 64 节点集群 Calico 部署测试

### 端到端测试

- 完整集群创建流程测试
- Calico 网络连通性测试
- 网络策略功能测试

### 性能测试

- Calico 部署时间测量
- 镜像拉取时间测量
- 网络初始化时间测量

## 风险与缓解

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|----------|
| 镜像预置失败 | 中 | 低 | 降级到正常拉取，记录错误日志 |
| VXLAN 模式兼容性问题 | 低 | 中 | 提供回退到 IPIP 模式的配置 |
| 并行部署导致资源竞争 | 中 | 中 | 设置合理的资源限制 |
| 禁用功能影响可观测性 | 低 | 低 | 保留健康检查功能，禁用其他非必要功能 |

## 实施历史

- **2026-07-13**：识别 Calico 部署慢问题
- **2026-07-14**：完成根因分析
- **2026-07-15**：制定优化方案
- **TBD**：实施镜像预置优化
- **TBD**：实施并行部署优化
- **TBD**：实施网络模式优化
- **TBD**：实施控制面优化
- **TBD**：实施配置优化
- **TBD**：完成测试验证

## 参考资料

1. [Calico 官方文档 - VXLAN 模式](https://docs.tigera.io/calico/latest/network-policy/configure/vxlan-tunnel)
2. [Kubernetes DaemonSet 更新策略](https://kubernetes.io/docs/tasks/manage-daemon/update-daemon-set/)
3. [BKE 64 节点集群性能瓶颈分析与优化方案](./64节点集群性能瓶颈分析与优化方案.md)
4. [KEP-10: 消除 API Throttling](./KEP-10-api-throttling-optimization.md)
5. [KEP-11: 优化健康检查收敛时间](./KEP-11-health-check-convergence.md)

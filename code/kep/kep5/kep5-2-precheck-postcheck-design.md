# KEP-5 补充设计：升级前预检与升级后检查

| 字段 | 值 |
|------|-----|
| **关联 KEP** | KEP-5 |
| **标题** | 升级前预检与升级后检查详细设计 |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-19 |
| **依赖** | KEP-5 声明式升级框架、三层状态机、可观测性设计 |

## 1. 摘要

本文档是 KEP-5 声明式升级框架的补充设计，定义升级前预检（Pre-Check）与升级后检查（Post-Check）的完整架构。通过插件化检查框架，在升级流程关键节点执行自动化验证，确保升级安全性和可观测性。

## 2. 动机

### 2.1 现状痛点

| 问题 | 现状 | 影响 |
|------|------|------|
| **缺乏升级前验证** | 升级前无自动化检查，依赖人工确认 | 升级失败率高，故障排查困难 |
| **升级后无验证** | 升级完成后无自动化健康检查 | 无法及时发现升级导致的隐性故障 |
| **检查结果不透明** | 无统一的检查报告机制 | 运维人员无法快速定位问题 |
| **检查项不可扩展** | 检查逻辑硬编码在控制器中 | 新增检查项需修改核心代码 |

### 2.2 目标

1. 设计插件化检查框架，支持预检项/后检项的动态注册与扩展
2. 定义升级前预检项清单，覆盖集群健康、备份验证、资源检查等
3. 定义升级后检查项清单，覆盖组件版本、集群健康、业务应用等
4. 实现检查执行引擎，支持并行/串行执行、超时控制、失败处理
5. 提供检查结果报告机制，支持持久化、事件记录、可观测性

### 2.3 非目标

1. 不替换现有的兼容性校验逻辑（`CheckCompatibility`），而是作为补充
2. 不在本文档定义具体的检查脚本实现，仅定义框架与接口
3. 不涉及 UI/CLI 层的检查结果展示

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| 检查框架设计 | CheckRunner、CheckRegistry、CheckItem 接口定义 |
| 预检项设计 | 集群健康、备份验证、路径验证、资源检查、依赖检查 |
| 后检项设计 | 版本验证、健康验证、节点验证、应用验证 |
| 执行引擎 | 并行/串行执行、超时控制、失败策略 |
| 结果报告 | CheckReport 聚合、持久化、事件记录 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 扩展现有 `CheckStep` 结构，不破坏已有定义 |
| **插件化** | 检查项通过注册机制接入，不侵入核心控制器 |
| **幂等性** | 检查项执行必须幂等，可重复执行 |
| **超时控制** | 单个检查项超时不应阻塞整体升级流程 |

## 4. 检查框架架构

### 4.1 整体架构

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          检查框架整体架构                                     │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│  ClusterVersionReconciler                                                   │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  Step 4: 执行预检 (PreCheck)                                        │   │
│  │  ┌──────────────────────────────────────────────────────────────┐  │   │
│  │  │  CheckRunner.RunPreChecks(ctx, cluster, upgradePath)         │  │   │
│  │  │  ├─ 从 UpgradePathEdge.PreCheck 获取检查项列表               │  │   │
│  │  │  ├─ 调用 CheckRegistry 解析检查项                            │  │   │
│  │  │  ├─ 并行/串行执行检查项                                      │  │   │
│  │  │  └─ 生成 CheckReport 并持久化                                │  │   │
│  │  └──────────────────────────────────────────────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  DAG 执行引擎                                                       │   │
│  │  ┌──────────────────────────────────────────────────────────────┐  │   │
│  │  │  executeDAG(ctx, cluster, dag)                                │  │   │
│  │  │  ├─ 按拓扑顺序执行组件升级                                    │  │   │
│  │  │  └─ 每个组件升级后执行组件级健康检查                           │  │   │
│  │  └──────────────────────────────────────────────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                                    ▼                                        │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  Step 5: 执行后检 (PostCheck)                                       │   │
│  │  ┌──────────────────────────────────────────────────────────────┐  │   │
│  │  │  CheckRunner.RunPostChecks(ctx, cluster, upgradePath)        │  │   │
│  │  │  ├─ 从 UpgradePathEdge.PostCheck 获取检查项列表              │  │   │
│  │  │  ├─ 调用 CheckRegistry 解析检查项                            │  │   │
│  │  │  ├─ 并行/串行执行检查项                                      │  │   │
│  │  │  └─ 生成 CheckReport 并持久化                                │  │   │
│  │  └──────────────────────────────────────────────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│  CheckRegistry (检查项注册表)                                               │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  registry: map[string]CheckItem                                      │   │
│  │  ├─ "cluster-health"       → ClusterHealthCheckItem                  │   │
│  │  ├─ "backup-verification"  → BackupVerificationCheckItem             │   │
│  │  ├─ "upgrade-path"         → UpgradePathCheckItem                    │   │
│  │  ├─ "resource-check"       → ResourceCheckItem                       │   │
│  │  ├─ "component-version"    → ComponentVersionCheckItem               │   │
│  │  ├─ "node-ready"           → NodeReadyCheckItem                      │   │
│  │  └─ "application-health"   → ApplicationHealthCheckItem              │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 与现有系统的集成

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  检查框架与现有系统集成                                                       │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│  UpgradePath CRD                                                            │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  spec.paths:                                                         │   │
│  │    - from: "v2.5.0"                                                  │   │
│  │      to: "v2.6.0"                                                    │   │
│  │      preCheck:                    ◀──── 预检项列表                    │   │
│  │        - name: "cluster-health"                                      │   │
│  │          required: true                                              │   │
│  │          timeout: "30s"                                              │   │
│  │        - name: "backup-verification"                                 │   │
│  │          required: true                                              │   │
│  │          timeout: "5m"                                               │   │
│  │        - name: "resource-check"                                      │   │
│  │          required: false                                             │   │
│  │      postCheck:                   ◀──── 后检项列表                    │   │
│  │        - name: "component-version"                                   │   │
│  │          required: true                                              │   │
│  │          timeout: "30s"                                              │   │
│  │        - name: "cluster-health"                                      │   │
│  │          required: true                                              │   │
│  │          timeout: "1m"                                               │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│  ClusterVersion Status                                                      │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  status:                                                             │   │
│  │    phase: "Upgrading"                                                │   │
│  │    preCheckReport:                ◀──── 预检报告                      │   │
│  │      status: "Passed"                                                │   │
│  │      results:                                                        │   │
│  │        - name: "cluster-health"                                      │   │
│  │          status: "Passed"                                            │   │
│  │          message: "All nodes are ready"                              │   │
│  │          duration: "2.5s"                                            │   │
│  │        - name: "backup-verification"                                 │   │
│  │          status: "Passed"                                            │   │
│  │          message: "Backup is valid"                                  │   │
│  │          duration: "45s"                                             │   │
│  │    postCheckReport:               ◀──── 后检报告                      │   │
│  │      status: "Passed"                                                │   │
│  │      results:                                                        │   │
│  │        - name: "component-version"                                   │   │
│  │          status: "Passed"                                            │   │
│  │          message: "All components are at target version"             │   │
│  │          duration: "1.2s"                                            │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 5. 数据结构设计

### 5.1 CheckStep 扩展

扩展现有 `CheckStep` 结构，增加执行参数与超时控制：

```go
// pkg/check/types.go

// CheckStep 检查步骤定义（扩展）
type CheckStep struct {
    // 检查项名称（对应 CheckRegistry 中的 key）
    Name string `json:"name"`
    
    // 是否必须通过（true=失败时阻断升级，false=仅告警）
    Required bool `json:"required,omitempty"`
    
    // 执行超时时间（默认 30s）
    Timeout string `json:"timeout,omitempty"`
    
    // 检查参数（传递给 CheckItem.Execute 的参数）
    Params map[string]string `json:"params,omitempty"`
    
    // 执行模式：Parallel / Sequential（默认 Sequential）
    Mode string `json:"mode,omitempty"`
    
    // 重试次数（默认 0，不重试）
    RetryCount int `json:"retryCount,omitempty"`
    
    // 重试间隔（默认 5s）
    RetryInterval string `json:"retryInterval,omitempty"`
}
```

### 5.2 CheckResult 结果类型

```go
// CheckStatus 检查状态
type CheckStatus string

const (
    CheckStatusPassed  CheckStatus = "Passed"
    CheckStatusFailed  CheckStatus = "Failed"
    CheckStatusSkipped CheckStatus = "Skipped"
    CheckStatusTimeout CheckStatus = "Timeout"
)

// CheckResult 单个检查项结果
type CheckResult struct {
    // 检查项名称
    Name string `json:"name"`
    
    // 检查状态
    Status CheckStatus `json:"status"`
    
    // 检查结果消息
    Message string `json:"message,omitempty"`
    
    // 执行耗时
    Duration string `json:"duration,omitempty"`
    
    // 错误详情（失败时）
    Error string `json:"error,omitempty"`
    
    // 检查项详情（可选，用于展示更多信息）
    Details map[string]interface{} `json:"details,omitempty"`
    
    // 执行时间
    ExecutedAt metav1.Time `json:"executedAt,omitempty"`
}
```

### 5.3 CheckReport 报告类型

```go
// CheckReport 检查报告（聚合多个检查结果）
type CheckReport struct {
    // 整体状态（所有 Required=true 的检查项都通过才算 Passed）
    Status CheckStatus `json:"status"`
    
    // 检查项结果列表
    Results []CheckResult `json:"results,omitempty"`
    
    // 总执行耗时
    TotalDuration string `json:"totalDuration,omitempty"`
    
    // 通过数量
    PassedCount int `json:"passedCount"`
    
    // 失败数量
    FailedCount int `json:"failedCount"`
    
    // 跳过数量
    SkippedCount int `json:"skippedCount"`
    
    // 执行时间
    ExecutedAt metav1.Time `json:"executedAt,omitempty"`
}

// ClusterVersionStatus 扩展（增加检查报告字段）
type ClusterVersionStatus struct {
    // ... 现有字段 ...
    
    // 预检报告
    PreCheckReport *CheckReport `json:"preCheckReport,omitempty"`
    
    // 后检报告
    PostCheckReport *CheckReport `json:"postCheckReport,omitempty"`
}
```

### 5.4 CheckItem 接口定义

```go
// CheckItem 检查项接口
type CheckItem interface {
    // Name 返回检查项名称
    Name() string
    
    // Execute 执行检查
    // ctx: 上下文
    // cluster: 集群实例
    // params: 检查参数（来自 CheckStep.Params）
    // 返回: 检查结果
    Execute(ctx context.Context, cluster *bkev1beta1.BKECluster, params map[string]string) (*CheckResult, error)
    
    // Description 返回检查项描述（用于文档和日志）
    Description() string
}
```

## 6. 检查执行引擎

### 6.1 CheckRegistry 注册表

```go
// pkg/check/registry.go

// CheckRegistry 检查项注册表
type CheckRegistry struct {
    mu       sync.RWMutex
    registry map[string]CheckItem
}

// NewCheckRegistry 创建注册表
func NewCheckRegistry() *CheckRegistry {
    return &CheckRegistry{
        registry: make(map[string]CheckItem),
    }
}

// Register 注册检查项
func (r *CheckRegistry) Register(item CheckItem) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.registry[item.Name()] = item
}

// Get 获取检查项
func (r *CheckRegistry) Get(name string) (CheckItem, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    item, ok := r.registry[name]
    return item, ok
}

// List 列出所有检查项
func (r *CheckRegistry) List() []CheckItem {
    r.mu.RLock()
    defer r.mu.RUnlock()
    items := make([]CheckItem, 0, len(r.registry))
    for _, item := range r.registry {
        items = append(items, item)
    }
    return items
}
```

### 6.2 CheckRunner 执行器

```go
// pkg/check/runner.go

// CheckRunner 检查执行器
type CheckRunner struct {
    registry *CheckRegistry
    logger   logr.Logger
}

// NewCheckRunner 创建执行器
func NewCheckRunner(registry *CheckRegistry, logger logr.Logger) *CheckRunner {
    return &CheckRunner{
        registry: registry,
        logger:   logger,
    }
}

// RunPreChecks 执行预检
func (r *CheckRunner) RunPreChecks(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    edge *cvoapi.UpgradePathEdge,
) (*CheckReport, error) {
    r.logger.Info("Running pre-checks", "from", edge.From, "to", edge.To)
    return r.runChecks(ctx, cluster, edge.PreCheck, "pre-check")
}

// RunPostChecks 执行后检
func (r *CheckRunner) RunPostChecks(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    edge *cvoapi.UpgradePathEdge,
) (*CheckReport, error) {
    r.logger.Info("Running post-checks", "from", edge.From, "to", edge.To)
    return r.runChecks(ctx, cluster, edge.PostCheck, "post-check")
}

// runChecks 执行检查项列表
func (r *CheckRunner) runChecks(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    steps []cvoapi.CheckStep,
    phase string,
) (*CheckReport, error) {
    report := &CheckReport{
        Status:     cvoapi.CheckStatusPassed,
        Results:    make([]cvoapi.CheckResult, 0, len(steps)),
        ExecutedAt: metav1.Now(),
    }
    
    startTime := time.Now()
    
    // 按执行模式分组
    parallelSteps := make([]cvoapi.CheckStep, 0)
    sequentialSteps := make([]cvoapi.CheckStep, 0)
    
    for _, step := range steps {
        if step.Mode == "Parallel" {
            parallelSteps = append(parallelSteps, step)
        } else {
            sequentialSteps = append(sequentialSteps, step)
        }
    }
    
    // 执行并行检查项
    if len(parallelSteps) > 0 {
        results, err := r.runParallelChecks(ctx, cluster, parallelSteps)
        if err != nil {
            return nil, err
        }
        report.Results = append(report.Results, results...)
    }
    
    // 执行串行检查项
    for _, step := range sequentialSteps {
        result, err := r.runSingleCheck(ctx, cluster, step)
        if err != nil {
            return nil, err
        }
        report.Results = append(report.Results, *result)
    }
    
    // 计算整体状态
    for _, result := range report.Results {
        if result.Status == cvoapi.CheckStatusFailed || result.Status == cvoapi.CheckStatusTimeout {
            report.FailedCount++
            // 查找对应的 step，判断是否 Required
            for _, step := range steps {
                if step.Name == result.Name && step.Required {
                    report.Status = cvoapi.CheckStatusFailed
                    break
                }
            }
        } else if result.Status == cvoapi.CheckStatusPassed {
            report.PassedCount++
        } else if result.Status == cvoapi.CheckStatusSkipped {
            report.SkippedCount++
        }
    }
    
    report.TotalDuration = time.Since(startTime).String()
    
    r.logger.Info("Check completed",
        "phase", phase,
        "status", report.Status,
        "passed", report.PassedCount,
        "failed", report.FailedCount,
        "duration", report.TotalDuration,
    )
    
    return report, nil
}

// runParallelChecks 并行执行检查项
func (r *CheckRunner) runParallelChecks(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    steps []cvoapi.CheckStep,
) ([]cvoapi.CheckResult, error) {
    var wg sync.WaitGroup
    results := make([]cvoapi.CheckResult, len(steps))
    errors := make([]error, len(steps))
    
    for i, step := range steps {
        wg.Add(1)
        go func(idx int, s cvoapi.CheckStep) {
            defer wg.Done()
            result, err := r.runSingleCheck(ctx, cluster, s)
            if err != nil {
                errors[idx] = err
                return
            }
            results[idx] = *result
        }(i, step)
    }
    
    wg.Wait()
    
    // 检查是否有错误
    for _, err := range errors {
        if err != nil {
            return nil, err
        }
    }
    
    return results, nil
}

// runSingleCheck 执行单个检查项
func (r *CheckRunner) runSingleCheck(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    step cvoapi.CheckStep,
) (*cvoapi.CheckResult, error) {
    item, ok := r.registry.Get(step.Name)
    if !ok {
        return &cvoapi.CheckResult{
            Name:       step.Name,
            Status:     cvoapi.CheckStatusSkipped,
            Message:    fmt.Sprintf("Check item %s not found in registry", step.Name),
            ExecutedAt: metav1.Now(),
        }, nil
    }
    
    // 解析超时时间
    timeout := 30 * time.Second
    if step.Timeout != "" {
        if d, err := time.ParseDuration(step.Timeout); err == nil {
            timeout = d
        }
    }
    
    // 创建带超时的上下文
    checkCtx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    
    startTime := time.Now()
    
    // 执行检查（支持重试）
    var result *cvoapi.CheckResult
    var lastErr error
    
    maxRetries := step.RetryCount
    if maxRetries < 0 {
        maxRetries = 0
    }
    
    for attempt := 0; attempt <= maxRetries; attempt++ {
        if attempt > 0 {
            // 重试间隔
            interval := 5 * time.Second
            if step.RetryInterval != "" {
                if d, err := time.ParseDuration(step.RetryInterval); err == nil {
                    interval = d
                }
            }
            time.Sleep(interval)
            r.logger.Info("Retrying check", "name", step.Name, "attempt", attempt+1)
        }
        
        var err error
        result, err = item.Execute(checkCtx, cluster, step.Params)
        if err == nil && result.Status == cvoapi.CheckStatusPassed {
            break
        }
        lastErr = err
    }
    
    if result == nil {
        result = &cvoapi.CheckResult{
            Name:       step.Name,
            Status:     cvoapi.CheckStatusFailed,
            Message:    "Check execution failed",
            ExecutedAt: metav1.Now(),
        }
    }
    
    // 检查是否超时
    if checkCtx.Err() == context.DeadlineExceeded {
        result.Status = cvoapi.CheckStatusTimeout
        result.Error = "Check execution timeout"
    }
    
    // 记录执行耗时
    result.Duration = time.Since(startTime).String()
    result.ExecutedAt = metav1.Now()
    
    // 如果有错误，记录到结果中
    if lastErr != nil && result.Error == "" {
        result.Error = lastErr.Error()
    }
    
    return result, nil
}
```

## 7. 升级前预检项设计

### 7.1 预检项清单

| 检查项 | 名称 | 说明 | 默认 Required | 默认 Timeout |
|--------|------|------|---------------|--------------|
| 集群健康检查 | `cluster-health` | 检查 etcd 成员状态、API Server 可用性、节点 Ready 比例 | true | 30s |
| 备份验证 | `backup-verification` | 验证 etcd 快照可用性、配置备份完整性 | true | 5m |
| 升级路径验证 | `upgrade-path` | 验证 UpgradePath 合法性、版本兼容性 | true | 10s |
| 资源检查 | `resource-check` | 检查磁盘空间、内存/CPU 余量、镜像预拉取 | false | 1m |
| 组件依赖检查 | `component-dependency` | 验证 ComponentVersion 兼容性约束 | true | 30s |

### 7.2 ClusterHealthCheckItem 实现

```go
// pkg/check/items/cluster_health.go

// ClusterHealthCheckItem 集群健康检查项
type ClusterHealthCheckItem struct {
    client client.Client
}

func (c *ClusterHealthCheckItem) Name() string {
    return "cluster-health"
}

func (c *ClusterHealthCheckItem) Description() string {
    return "Check cluster health: etcd members, API server, node readiness"
}

func (c *ClusterHealthCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*cvoapi.CheckResult, error) {
    result := &cvoapi.CheckResult{
        Name:       c.Name(),
        Status:     cvoapi.CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    // 1. 检查 etcd 成员状态
    etcdHealthy, etcdMsg, err := c.checkEtcdHealth(ctx, cluster)
    if err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("etcd health check failed: %v", err)
        return result, nil
    }
    result.Details["etcd"] = map[string]interface{}{
        "healthy": etcdHealthy,
        "message": etcdMsg,
    }
    
    // 2. 检查 API Server 可用性
    apiHealthy, apiMsg, err := c.checkAPIServerHealth(ctx, cluster)
    if err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("API server health check failed: %v", err)
        return result, nil
    }
    result.Details["apiServer"] = map[string]interface{}{
        "healthy": apiHealthy,
        "message": apiMsg,
    }
    
    // 3. 检查节点 Ready 比例
    nodeHealthy, nodeMsg, readyRatio, err := c.checkNodeReadiness(ctx, cluster)
    if err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("node readiness check failed: %v", err)
        return result, nil
    }
    result.Details["nodes"] = map[string]interface{}{
        "healthy":    nodeHealthy,
        "message":    nodeMsg,
        "readyRatio": readyRatio,
    }
    
    // 综合判断
    if !etcdHealthy || !apiHealthy || !nodeHealthy {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = "Cluster health check failed"
    } else {
        result.Message = "Cluster is healthy"
    }
    
    return result, nil
}

func (c *ClusterHealthCheckItem) checkEtcdHealth(ctx context.Context, cluster *bkev1beta1.BKECluster) (bool, string, error) {
    // 获取 etcd Pod 列表
    podList := &corev1.PodList{}
    if err := c.client.List(ctx, podList,
        client.InNamespace("kube-system"),
        client.MatchingLabels{"component": "etcd"},
    ); err != nil {
        return false, "", err
    }
    
    if len(podList.Items) == 0 {
        return false, "No etcd pods found", nil
    }
    
    // 检查所有 etcd Pod 是否 Running
    for _, pod := range podList.Items {
        if pod.Status.Phase != corev1.PodRunning {
            return false, fmt.Sprintf("etcd pod %s is not running", pod.Name), nil
        }
    }
    
    return true, fmt.Sprintf("All %d etcd pods are running", len(podList.Items)), nil
}

func (c *ClusterHealthCheckItem) checkAPIServerHealth(ctx context.Context, cluster *bkev1beta1.BKECluster) (bool, string, error) {
    // 获取 API Server Pod 列表
    podList := &corev1.PodList{}
    if err := c.client.List(ctx, podList,
        client.InNamespace("kube-system"),
        client.MatchingLabels{"component": "kube-apiserver"},
    ); err != nil {
        return false, "", err
    }
    
    if len(podList.Items) == 0 {
        return false, "No API server pods found", nil
    }
    
    // 检查所有 API Server Pod 是否 Running
    for _, pod := range podList.Items {
        if pod.Status.Phase != corev1.PodRunning {
            return false, fmt.Sprintf("API server pod %s is not running", pod.Name), nil
        }
    }
    
    return true, fmt.Sprintf("All %d API server pods are running", len(podList.Items)), nil
}

func (c *ClusterHealthCheckItem) checkNodeReadiness(ctx context.Context, cluster *bkev1beta1.BKECluster) (bool, string, float64, error) {
    // 获取所有节点
    nodeList := &corev1.NodeList{}
    if err := c.client.List(ctx, nodeList); err != nil {
        return false, "", 0, err
    }
    
    if len(nodeList.Items) == 0 {
        return false, "No nodes found", 0, nil
    }
    
    readyCount := 0
    for _, node := range nodeList.Items {
        for _, condition := range node.Status.Conditions {
            if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
                readyCount++
                break
            }
        }
    }
    
    readyRatio := float64(readyCount) / float64(len(nodeList.Items))
    
    // 要求至少 80% 节点 Ready
    if readyRatio < 0.8 {
        return false, fmt.Sprintf("Only %d/%d nodes are ready (%.1f%%)", readyCount, len(nodeList.Items), readyRatio*100), readyRatio, nil
    }
    
    return true, fmt.Sprintf("%d/%d nodes are ready (%.1f%%)", readyCount, len(nodeList.Items), readyRatio*100), readyRatio, nil
}
```

### 7.3 BackupVerificationCheckItem 实现

```go
// pkg/check/items/backup_verification.go

// BackupVerificationCheckItem 备份验证检查项
type BackupVerificationCheckItem struct {
    client client.Client
}

func (b *BackupVerificationCheckItem) Name() string {
    return "backup-verification"
}

func (b *BackupVerificationCheckItem) Description() string {
    return "Verify etcd snapshot and configuration backup availability"
}

func (b *BackupVerificationCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*cvoapi.CheckResult, error) {
    result := &cvoapi.CheckResult{
        Name:       b.Name(),
        Status:     cvoapi.CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    // 1. 检查最近的 etcd 备份
    backupList := &bkev1beta1.EtcdBackupList{}
    if err := b.client.List(ctx, backupList,
        client.InNamespace(cluster.Namespace),
    ); err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("Failed to list etcd backups: %v", err)
        return result, nil
    }
    
    if len(backupList.Items) == 0 {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = "No etcd backups found"
        return result, nil
    }
    
    // 找到最近的备份
    var latestBackup *bkev1beta1.EtcdBackup
    for i := range backupList.Items {
        backup := &backupList.Items[i]
        if latestBackup == nil || backup.CreationTimestamp.After(latestBackup.CreationTimestamp.Time) {
            latestBackup = backup
        }
    }
    
    // 检查备份是否在 24 小时内
    backupAge := time.Since(latestBackup.CreationTimestamp.Time)
    if backupAge > 24*time.Hour {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = fmt.Sprintf("Latest backup is too old: %v", backupAge)
        return result, nil
    }
    
    // 检查备份状态
    if latestBackup.Status.Phase != bkev1beta1.BackupPhaseCompleted {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = fmt.Sprintf("Latest backup is not completed: %s", latestBackup.Status.Phase)
        return result, nil
    }
    
    result.Details["latestBackup"] = map[string]interface{}{
        "name":      latestBackup.Name,
        "createdAt": latestBackup.CreationTimestamp.Time,
        "age":       backupAge.String(),
        "status":    latestBackup.Status.Phase,
    }
    
    result.Message = fmt.Sprintf("Latest backup is valid (age: %v)", backupAge.Round(time.Minute))
    
    return result, nil
}
```

### 7.4 UpgradePathCheckItem 实现

```go
// pkg/check/items/upgrade_path.go

// UpgradePathCheckItem 升级路径验证检查项
type UpgradePathCheckItem struct {
    upgradePathGraph *upgrade.UpgradePathGraph
}

func (u *UpgradePathCheckItem) Name() string {
    return "upgrade-path"
}

func (u *UpgradePathCheckItem) Description() string {
    return "Verify upgrade path legality and version compatibility"
}

func (u *UpgradePathCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*cvoapi.CheckResult, error) {
    result := &cvoapi.CheckResult{
        Name:       u.Name(),
        Status:     cvoapi.CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    // 获取当前版本和目标版本
    currentVersion := params["currentVersion"]
    targetVersion := params["targetVersion"]
    
    if currentVersion == "" || targetVersion == "" {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = "currentVersion and targetVersion are required"
        return result, nil
    }
    
    // 查找升级路径
    path, err := u.upgradePathGraph.FindPath(currentVersion, targetVersion)
    if err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("No valid upgrade path from %s to %s: %v", currentVersion, targetVersion, err)
        return result, nil
    }
    
    // 检查路径中是否有 blocked 边
    for _, edge := range path {
        if edge.Blocked {
            result.Status = cvoapi.CheckStatusFailed
            result.Message = fmt.Sprintf("Upgrade path is blocked at %s -> %s", edge.From, edge.To)
            return result, nil
        }
    }
    
    result.Details["path"] = map[string]interface{}{
        "from":          currentVersion,
        "to":            targetVersion,
        "hops":          len(path),
        "path":          path,
    }
    
    result.Message = fmt.Sprintf("Valid upgrade path found: %s -> %s (%d hops)", currentVersion, targetVersion, len(path))
    
    return result, nil
}
```

### 7.5 ResourceCheckItem 实现

```go
// pkg/check/items/resource_check.go

// ResourceCheckItem 资源检查项
type ResourceCheckItem struct {
    client client.Client
}

func (r *ResourceCheckItem) Name() string {
    return "resource-check"
}

func (r *ResourceCheckItem) Description() string {
    return "Check disk space, memory/CPU availability, and image pre-pull"
}

func (r *ResourceCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*cvoapi.CheckResult, error) {
    result := &cvoapi.CheckResult{
        Name:       r.Name(),
        Status:     cvoapi.CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    // 1. 检查磁盘空间（通过 SSH 执行 df 命令）
    diskOK, diskMsg, err := r.checkDiskSpace(ctx, cluster)
    if err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("Disk space check failed: %v", err)
        return result, nil
    }
    result.Details["disk"] = map[string]interface{}{
        "ok":      diskOK,
        "message": diskMsg,
    }
    
    // 2. 检查内存余量
    memOK, memMsg, err := r.checkMemory(ctx, cluster)
    if err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("Memory check failed: %v", err)
        return result, nil
    }
    result.Details["memory"] = map[string]interface{}{
        "ok":      memOK,
        "message": memMsg,
    }
    
    // 3. 检查 CPU 余量
    cpuOK, cpuMsg, err := r.checkCPU(ctx, cluster)
    if err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("CPU check failed: %v", err)
        return result, nil
    }
    result.Details["cpu"] = map[string]interface{}{
        "ok":      cpuOK,
        "message": cpuMsg,
    }
    
    // 综合判断
    if !diskOK || !memOK || !cpuOK {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = "Resource check failed"
    } else {
        result.Message = "Resource check passed"
    }
    
    return result, nil
}

func (r *ResourceCheckItem) checkDiskSpace(ctx context.Context, cluster *bkev1beta1.BKECluster) (bool, string, error) {
    // 通过 SSH 执行 df -h /var/lib/etcd
    // 要求磁盘使用率 < 80%
    // 实现略...
    return true, "Disk usage is acceptable", nil
}

func (r *ResourceCheckItem) checkMemory(ctx context.Context, cluster *bkev1beta1.BKECluster) (bool, string, error) {
    // 通过 Node metrics 检查内存使用率
    // 要求内存使用率 < 85%
    // 实现略...
    return true, "Memory usage is acceptable", nil
}

func (r *ResourceCheckItem) checkCPU(ctx context.Context, cluster *bkev1beta1.BKECluster) (bool, string, error) {
    // 通过 Node metrics 检查 CPU 使用率
    // 要求 CPU 使用率 < 80%
    // 实现略...
    return true, "CPU usage is acceptable", nil
}
```

### 7.6 ComponentDependencyCheckItem 实现

```go
// pkg/check/items/component_dependency.go

// ComponentDependencyCheckItem 组件依赖检查项
type ComponentDependencyCheckItem struct {
    manifestStore *manifest.ManifestStore
}

func (c *ComponentDependencyCheckItem) Name() string {
    return "component-dependency"
}

func (c *ComponentDependencyCheckItem) Description() string {
    return "Verify ComponentVersion compatibility constraints"
}

func (c *ComponentDependencyCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*cvoapi.CheckResult, error) {
    result := &cvoapi.CheckResult{
        Name:       c.Name(),
        Status:     cvoapi.CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    // 获取目标版本的 ReleaseImage
    targetVersion := params["targetVersion"]
    if targetVersion == "" {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = "targetVersion is required"
        return result, nil
    }
    
    targetRI, err := c.manifestStore.GetReleaseImage(targetVersion)
    if err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("Failed to get ReleaseImage for %s: %v", targetVersion, err)
        return result, nil
    }
    
    // 扁平化所有组件
    var components []manifest.ComponentRef
    for _, comp := range targetRI.Spec.Install.Components {
        components = append(components, manifest.ComponentRef{Name: comp.Name, Version: comp.Version})
    }
    for _, comp := range targetRI.Spec.Upgrade.Components {
        components = append(components, manifest.ComponentRef{Name: comp.Name, Version: comp.Version})
    }
    
    // 执行兼容性检查
    if err := manifest.CheckCompatibility(components, c.manifestStore); err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("Compatibility check failed: %v", err)
        return result, nil
    }
    
    result.Details["components"] = map[string]interface{}{
        "count": len(components),
    }
    
    result.Message = fmt.Sprintf("All %d components passed compatibility check", len(components))
    
    return result, nil
}
```

## 8. 升级后检查项设计

### 8.1 后检项清单

| 检查项 | 名称 | 说明 | 默认 Required | 默认 Timeout |
|--------|------|------|---------------|--------------|
| 组件版本验证 | `component-version` | 验证所有组件版本一致性 | true | 30s |
| 集群健康验证 | `cluster-health` | 验证 etcd/apiserver/controller-manager/scheduler 状态 | true | 1m |
| 节点状态验证 | `node-ready` | 验证所有节点 Ready | true | 30s |
| 业务应用验证 | `application-health` | 验证关键 Deployment/DaemonSet 就绪 | false | 2m |

### 8.2 ComponentVersionCheckItem 实现

```go
// pkg/check/items/component_version.go

// ComponentVersionCheckItem 组件版本验证检查项
type ComponentVersionCheckItem struct {
    client        client.Client
    manifestStore *manifest.ManifestStore
}

func (c *ComponentVersionCheckItem) Name() string {
    return "component-version"
}

func (c *ComponentVersionCheckItem) Description() string {
    return "Verify all components are at target version"
}

func (c *ComponentVersionCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*cvoapi.CheckResult, error) {
    result := &cvoapi.CheckResult{
        Name:       c.Name(),
        Status:     cvoapi.CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    // 获取目标版本
    targetVersion := params["targetVersion"]
    if targetVersion == "" {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = "targetVersion is required"
        return result, nil
    }
    
    // 获取目标版本的 ReleaseImage
    targetRI, err := c.manifestStore.GetReleaseImage(targetVersion)
    if err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("Failed to get ReleaseImage for %s: %v", targetVersion, err)
        return result, nil
    }
    
    // 检查每个组件的当前版本
    mismatches := make([]map[string]string, 0)
    
    for _, comp := range targetRI.Spec.Install.Components {
        currentVersion := c.getComponentCurrentVersion(ctx, cluster, comp.Name)
        if currentVersion != comp.Version {
            mismatches = append(mismatches, map[string]string{
                "component":       comp.Name,
                "targetVersion":   comp.Version,
                "currentVersion":  currentVersion,
            })
        }
    }
    
    for _, comp := range targetRI.Spec.Upgrade.Components {
        currentVersion := c.getComponentCurrentVersion(ctx, cluster, comp.Name)
        if currentVersion != comp.Version {
            mismatches = append(mismatches, map[string]string{
                "component":       comp.Name,
                "targetVersion":   comp.Version,
                "currentVersion":  currentVersion,
            })
        }
    }
    
    if len(mismatches) > 0 {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = fmt.Sprintf("%d components are not at target version", len(mismatches))
        result.Details["mismatches"] = mismatches
    } else {
        result.Message = "All components are at target version"
    }
    
    return result, nil
}

func (c *ComponentVersionCheckItem) getComponentCurrentVersion(ctx context.Context, cluster *bkev1beta1.BKECluster, componentName string) string {
    // 从 ClusterVersion.Status 或 BKECluster.Status 获取组件当前版本
    // 实现略...
    return ""
}
```

### 8.3 NodeReadyCheckItem 实现

```go
// pkg/check/items/node_ready.go

// NodeReadyCheckItem 节点就绪检查项
type NodeReadyCheckItem struct {
    client client.Client
}

func (n *NodeReadyCheckItem) Name() string {
    return "node-ready"
}

func (n *NodeReadyCheckItem) Description() string {
    return "Verify all nodes are in Ready state"
}

func (n *NodeReadyCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*cvoapi.CheckResult, error) {
    result := &cvoapi.CheckResult{
        Name:       n.Name(),
        Status:     cvoapi.CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    // 获取所有节点
    nodeList := &corev1.NodeList{}
    if err := n.client.List(ctx, nodeList); err != nil {
        result.Status = cvoapi.CheckStatusFailed
        result.Error = fmt.Sprintf("Failed to list nodes: %v", err)
        return result, nil
    }
    
    notReadyNodes := make([]string, 0)
    for _, node := range nodeList.Items {
        ready := false
        for _, condition := range node.Status.Conditions {
            if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
                ready = true
                break
            }
        }
        if !ready {
            notReadyNodes = append(notReadyNodes, node.Name)
        }
    }
    
    if len(notReadyNodes) > 0 {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = fmt.Sprintf("%d nodes are not ready", len(notReadyNodes))
        result.Details["notReadyNodes"] = notReadyNodes
    } else {
        result.Message = fmt.Sprintf("All %d nodes are ready", len(nodeList.Items))
    }
    
    return result, nil
}
```

### 8.4 ApplicationHealthCheckItem 实现

```go
// pkg/check/items/application_health.go

// ApplicationHealthCheckItem 应用健康检查项
type ApplicationHealthCheckItem struct {
    client client.Client
}

func (a *ApplicationHealthCheckItem) Name() string {
    return "application-health"
}

func (a *ApplicationHealthCheckItem) Description() string {
    return "Verify critical Deployments and DaemonSets are ready"
}

func (a *ApplicationHealthCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*cvoapi.CheckResult, error) {
    result := &cvoapi.CheckResult{
        Name:       a.Name(),
        Status:     cvoapi.CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    // 检查关键 Deployment
    criticalDeployments := []struct {
        namespace string
        name      string
    }{
        {"kube-system", "coredns"},
        {"kube-system", "kube-proxy"},
    }
    
    unhealthyDeployments := make([]string, 0)
    for _, dep := range criticalDeployments {
        deployment := &appsv1.Deployment{}
        if err := a.client.Get(ctx, client.ObjectKey{Namespace: dep.namespace, Name: dep.name}, deployment); err != nil {
            unhealthyDeployments = append(unhealthyDeployments, fmt.Sprintf("%s/%s (not found)", dep.namespace, dep.name))
            continue
        }
        
        if deployment.Status.ReadyReplicas != deployment.Status.Replicas {
            unhealthyDeployments = append(unhealthyDeployments, fmt.Sprintf("%s/%s (%d/%d ready)", dep.namespace, dep.name, deployment.Status.ReadyReplicas, deployment.Status.Replicas))
        }
    }
    
    // 检查关键 DaemonSet
    criticalDaemonSets := []struct {
        namespace string
        name      string
    }{
        {"kube-system", "calico-node"},
    }
    
    unhealthyDaemonSets := make([]string, 0)
    for _, ds := range criticalDaemonSets {
        daemonSet := &appsv1.DaemonSet{}
        if err := a.client.Get(ctx, client.ObjectKey{Namespace: ds.namespace, Name: ds.name}, daemonSet); err != nil {
            unhealthyDaemonSets = append(unhealthyDaemonSets, fmt.Sprintf("%s/%s (not found)", ds.namespace, ds.name))
            continue
        }
        
        if daemonSet.Status.NumberReady != daemonSet.Status.DesiredNumberScheduled {
            unhealthyDaemonSets = append(unhealthyDaemonSets, fmt.Sprintf("%s/%s (%d/%d ready)", ds.namespace, ds.name, daemonSet.Status.NumberReady, daemonSet.Status.DesiredNumberScheduled))
        }
    }
    
    if len(unhealthyDeployments) > 0 || len(unhealthyDaemonSets) > 0 {
        result.Status = cvoapi.CheckStatusFailed
        result.Message = "Some critical applications are not ready"
        result.Details["unhealthyDeployments"] = unhealthyDeployments
        result.Details["unhealthyDaemonSets"] = unhealthyDaemonSets
    } else {
        result.Message = "All critical applications are ready"
    }
    
    return result, nil
}
```

## 9. 检查结果报告

### 9.1 CheckReport 聚合结构

```go
// CheckReport 已在 5.3 节定义，此处展示使用示例

// 预检报告示例
preCheckReport := &cvoapi.CheckReport{
    Status:     cvoapi.CheckStatusPassed,
    Results: []cvoapi.CheckResult{
        {
            Name:       "cluster-health",
            Status:     cvoapi.CheckStatusPassed,
            Message:    "Cluster is healthy",
            Duration:   "2.5s",
            ExecutedAt: metav1.Now(),
        },
        {
            Name:       "backup-verification",
            Status:     cvoapi.CheckStatusPassed,
            Message:    "Latest backup is valid (age: 2h)",
            Duration:   "45s",
            ExecutedAt: metav1.Now(),
        },
    },
    TotalDuration:  "47.5s",
    PassedCount:    2,
    FailedCount:    0,
    SkippedCount:   0,
    ExecutedAt:     metav1.Now(),
}
```

### 9.2 结果持久化

```go
// 将检查报告写入 ClusterVersion.Status

func (r *ClusterVersionReconciler) updatePreCheckReport(
    ctx context.Context,
    cv *cvoapi.ClusterVersion,
    report *cvoapi.CheckReport,
) error {
    cv.Status.PreCheckReport = report
    return r.Status().Update(ctx, cv)
}

func (r *ClusterVersionReconciler) updatePostCheckReport(
    ctx context.Context,
    cv *cvoapi.ClusterVersion,
    report *cvoapi.CheckReport,
) error {
    cv.Status.PostCheckReport = report
    return r.Status().Update(ctx, cv)
}
```

### 9.3 Event 事件记录

```go
// 记录检查事件

func (r *ClusterVersionReconciler) recordCheckEvent(
    cv *cvoapi.ClusterVersion,
    phase string,
    report *cvoapi.CheckReport,
) {
    eventType := v1.EventTypeNormal
    reason := "CheckPassed"
    
    if report.Status == cvoapi.CheckStatusFailed {
        eventType = v1.EventTypeWarning
        reason = "CheckFailed"
    }
    
    r.Recorder.Eventf(
        cv,
        eventType,
        reason,
        "%s completed: %d passed, %d failed, %d skipped (duration: %s)",
        phase,
        report.PassedCount,
        report.FailedCount,
        report.SkippedCount,
        report.TotalDuration,
    )
}
```

### 9.4 可观测性指标

```go
// pkg/check/metrics.go

var (
    // 检查执行次数
    checkExecutionTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "bke_check_execution_total",
            Help: "Total number of check executions",
        },
        []string{"check_name", "phase", "status"},
    )
    
    // 检查执行耗时
    checkDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "bke_check_duration_seconds",
            Help:    "Duration of check execution in seconds",
            Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
        },
        []string{"check_name", "phase"},
    )
    
    // 检查通过/失败率
    checkSuccessRate = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "bke_check_success_rate",
            Help: "Check success rate (0.0 to 1.0)",
        },
        []string{"check_name", "phase"},
    )
)

func init() {
    prometheus.MustRegister(checkExecutionTotal)
    prometheus.MustRegister(checkDurationSeconds)
    prometheus.MustRegister(checkSuccessRate)
}

// 在 CheckRunner 中记录指标

func (r *CheckRunner) recordMetrics(result *cvoapi.CheckResult, phase string) {
    checkExecutionTotal.WithLabelValues(result.Name, phase, string(result.Status)).Inc()
    
    if duration, err := time.ParseDuration(result.Duration); err == nil {
        checkDurationSeconds.WithLabelValues(result.Name, phase).Observe(duration.Seconds())
    }
}
```

## 10. 与现有系统集成

### 10.1 ClusterVersionReconciler 集成

```go
// 在 ClusterVersionReconciler.Reconcile 中集成检查框架

func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &cvoapi.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // ... 现有逻辑 ...
    
    // Step 4: 执行预检
    edge, err := r.upgradePathGraph.FindPath(cv.Status.CurrentVersion, cv.Spec.DesiredVersion)
    if err != nil {
        return r.updateStatus(ctx, cv, cvoapi.PhaseBlocked, "no valid upgrade path")
    }
    
    preCheckReport, err := r.checkRunner.RunPreChecks(ctx, cluster, edge)
    if err != nil {
        return r.updateStatus(ctx, cv, cvoapi.PhaseFailed, fmt.Sprintf("pre-check failed: %v", err))
    }
    
    // 持久化预检报告
    cv.Status.PreCheckReport = preCheckReport
    r.recordCheckEvent(cv, "PreCheck", preCheckReport)
    
    // 如果预检失败且 Required=true，阻断升级
    if preCheckReport.Status == cvoapi.CheckStatusFailed {
        return r.updateStatus(ctx, cv, cvoapi.PhasePreCheckFailed, "pre-check failed")
    }
    
    // Step 5: 触发 BKECluster 调谐（执行 DAG）
    // ... 现有逻辑 ...
    
    // Step 6: DAG 执行完成后，执行后检
    postCheckReport, err := r.checkRunner.RunPostChecks(ctx, cluster, edge)
    if err != nil {
        r.Log.Error(err, "post-check failed")
        // 后检失败不阻断升级，但记录警告
        r.Recorder.Eventf(cv, v1.EventTypeWarning, "PostCheckFailed", "post-check failed: %v", err)
    } else {
        // 持久化后检报告
        cv.Status.PostCheckReport = postCheckReport
        r.recordCheckEvent(cv, "PostCheck", postCheckReport)
    }
    
    return r.updateStatus(ctx, cv, cvoapi.PhaseReady, "")
}
```

### 10.2 检查项注册

```go
// cmd/manager/main.go

func main() {
    // ... 初始化控制器 ...
    
    // 创建检查注册表
    checkRegistry := check.NewCheckRegistry()
    
    // 注册预检项
    checkRegistry.Register(&items.ClusterHealthCheckItem{Client: mgr.GetClient()})
    checkRegistry.Register(&items.BackupVerificationCheckItem{Client: mgr.GetClient()})
    checkRegistry.Register(&items.UpgradePathCheckItem{UpgradePathGraph: upgradePathGraph})
    checkRegistry.Register(&items.ResourceCheckItem{Client: mgr.GetClient()})
    checkRegistry.Register(&items.ComponentDependencyCheckItem{ManifestStore: manifestStore})
    
    // 注册后检项
    checkRegistry.Register(&items.ComponentVersionCheckItem{Client: mgr.GetClient(), ManifestStore: manifestStore})
    checkRegistry.Register(&items.NodeReadyCheckItem{Client: mgr.GetClient()})
    checkRegistry.Register(&items.ApplicationHealthCheckItem{Client: mgr.GetClient()})
    
    // 创建检查执行器
    checkRunner := check.NewCheckRunner(checkRegistry, mgr.GetLogger())
    
    // 注入到 ClusterVersionReconciler
    cvReconciler := &ClusterVersionReconciler{
        Client:         mgr.GetClient(),
        checkRunner:    checkRunner,
        upgradePathGraph: upgradePathGraph,
        manifestStore:  manifestStore,
    }
    
    // ... 启动控制器 ...
}
```

## 11. 测试设计

### 11.1 单元测试

| 测试场景 | 测试内容 | 预期结果 |
|---------|---------|---------|
| CheckRegistry 注册 | 注册检查项并获取 | 正确返回检查项 |
| CheckRunner 执行 | 执行单个检查项 | 返回正确的 CheckResult |
| 并行执行 | 并行执行多个检查项 | 所有检查项并行完成 |
| 超时控制 | 检查项执行超时 | 返回 Timeout 状态 |
| 重试机制 | 检查项失败后重试 | 按指定次数重试 |
| Required 判断 | Required=true 失败 | 整体状态为 Failed |
| Required 判断 | Required=false 失败 | 整体状态为 Passed |

### 11.2 集成测试

| 测试场景 | 测试内容 | 预期结果 |
|---------|---------|---------|
| 预检集成 | ClusterVersionReconciler 执行预检 | 预检报告写入 Status |
| 后检集成 | DAG 执行完成后执行后检 | 后检报告写入 Status |
| 预检失败阻断 | Required=true 预检失败 | 升级被阻断 |
| 预检告警 | Required=false 预检失败 | 升级继续，记录告警 |
| Event 记录 | 检查完成记录 Event | 正确记录 Event |

### 11.3 E2E 测试

| 测试场景 | 测试内容 | 预期结果 |
|---------|---------|---------|
| 完整升级流程 | 预检 → DAG 执行 → 后检 | 全流程通过 |
| 预检失败场景 | 模拟 etcd 不健康 | 升级被阻断 |
| 后检失败场景 | 模拟组件版本不一致 | 升级完成，记录告警 |

## 12. 工作量评估

| 阶段 | 任务内容 | 工作量 (人天) | 说明 |
|------|---------|-------------|------|
| **1. 框架设计** | CheckItem 接口、CheckRegistry、CheckRunner | 5 | 核心框架实现 |
| **2. 预检项开发** | 5 个预检项实现 | 8 | 集群健康、备份验证、路径验证、资源检查、依赖检查 |
| **3. 后检项开发** | 4 个后检项实现 | 6 | 版本验证、健康验证、节点验证、应用验证 |
| **4. 集成开发** | ClusterVersionReconciler 集成、Event 记录 | 4 | 与现有控制器集成 |
| **5. 可观测性** | Prometheus 指标、Grafana Dashboard | 3 | 监控与可视化 |
| **6. 测试** | 单元测试、集成测试、E2E 测试 | 5 | 测试覆盖 |
| **总计** | | **31 人天** | 约 1.5 个月 |

## 13. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 检查项执行超时 | 升级流程阻塞 | 设置合理超时时间，支持跳过非关键检查 |
| 检查项误报 | 升级被错误阻断 | 支持 Required=false 告警模式，支持手动跳过 |
| 检查项漏报 | 升级后发现问题 | 后检项覆盖关键组件，支持手动触发重新检查 |
| 检查项性能 | 升级耗时增加 | 并行执行检查项，缓存检查结果 |

---

**文档版本**: v1.0  
**维护者**: openFuyao Team
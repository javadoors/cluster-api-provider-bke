# KEP-5 补充设计 v2：升级前预检与升级后检查（重构版）

| 字段 | 值 |
|------|-----|
| **关联 KEP** | KEP-5 |
| **标题** | 升级前预检与升级后检查详细设计（重构版） |
| **状态** | `provisional` |
| **类型** | Feature |
| **作者** | openFuyao Team |
| **创建日期** | 2026-08-20 |
| **依赖** | KEP-5 声明式升级框架、三层状态机、可观测性设计 |

## 1. 摘要

本文档是 KEP-5 声明式升级框架的补充设计，定义升级前预检（Pre-Check）与升级后检查（Post-Check）的完整架构。通过引入独立的 `CheckPolicy` CRD，将检查策略与升级路径解耦，实现更灵活、可维护的检查框架。

## 2. 动机

### 2.1 现状痛点

| 问题 | 说明 | 影响 |
|------|------|------|
| **缺乏升级前验证** | 升级前无自动化检查，依赖人工确认 | 升级失败率高，故障排查困难 |
| **升级后无验证** | 升级完成后无自动化健康检查 | 无法及时发现升级导致的隐性故障 |
| **检查结果不透明** | 无统一的检查报告机制 | 运维人员无法快速定位问题 |
| **检查项不可扩展** | 检查逻辑硬编码在控制器中 | 新增检查项需修改核心代码 |

### 2.2 目标

1. **解耦设计**：将检查策略从 UpgradePath 中解耦，引入独立的 `CheckPolicy` CRD
2. **灵活配置**：支持按集群/环境定制检查策略（通过 LabelSelector + Priority）
3. **插件化**：检查项通过注册机制接入，支持动态扩展
4. **可观测**：检查结果持久化，支持事件记录和 Prometheus 指标

### 2.3 非目标

1. 不替换现有的兼容性校验逻辑（`CheckCompatibility`），而是作为补充
2. 不在本文档定义具体的检查脚本实现，仅定义框架与接口
3. 不涉及 UI/CLI 层的检查结果展示

## 3. 范围与约束

### 3.1 范围

| 范围 | 说明 |
|------|------|
| CheckPolicy CRD | 独立的检查策略 CRD 设计 |
| 检查框架设计 | CheckRunner、CheckRegistry、CheckItem 接口定义 |
| 策略解析机制 | 按集群标签匹配 + 优先级选择 |
| 预检项设计 | 集群健康、备份验证、资源检查、依赖检查 |
| 后检项设计 | 版本验证、健康验证、节点验证、应用验证 |
| 执行引擎 | 并行/串行执行、超时控制、失败策略 |
| 结果报告 | CheckReport 聚合、持久化、事件记录 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **职责单一** | UpgradePath 仅定义版本路径合法性，CheckPolicy 独立管理检查策略 |
| **插件化** | 检查项通过注册机制接入，不侵入核心控制器 |
| **幂等性** | 检查项执行必须幂等，可重复执行 |
| **超时控制** | 单个检查项超时不应阻塞整体升级流程 |

## 4. 架构设计

### 4.1 整体架构

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          检查框架整体架构                                     │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│  CheckPolicy CRD (独立资源)                                                  │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  CheckPolicy "default" (全局默认)                                    │   │
│  │  ┌──────────────────────────────────────────────────────────────┐  │   │
│  │  │  spec:                                                        │  │   │
│  │  │    preCheck:                                                  │  │   │
│  │  │      - name: "cluster-health"                                 │  │   │
│  │  │        required: true                                         │  │   │
│  │  │      - name: "backup-verification"                            │  │   │
│  │  │        required: true                                         │  │   │
│  │  │      - name: "resource-check"                                 │  │   │
│  │  │        required: false                                        │  │   │
│  │  │    postCheck:                                                 │  │   │
│  │  │      - name: "component-version"                              │  │   │
│  │  │        required: true                                         │  │   │
│  │  │      - name: "cluster-health"                                 │  │   │
│  │  │        required: true                                         │  │   │
│  │  └──────────────────────────────────────────────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  CheckPolicy "production" (生产环境，priority=10)                    │   │
│  │  ┌──────────────────────────────────────────────────────────────┐  │   │
│  │  │  spec:                                                        │  │   │
│  │  │    selector:                                                  │  │   │
│  │  │      matchLabels: { environment: production }                 │  │   │
│  │  │    priority: 10                                               │  │   │
│  │  │    preCheck:                                                  │  │   │
│  │  │      - name: "resource-check"                                 │  │   │
│  │  │        required: true   # 生产环境更严格                        │  │   │
│  │  │      ...                                                      │  │   │
│  │  └──────────────────────────────────────────────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      │ 按标签匹配 + 优先级选择
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  CheckPolicyResolver (策略解析器)                                            │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  1. 列出所有 CheckPolicy                                            │   │
│  │  2. 按 Selector 筛选匹配当前集群的策略                               │   │
│  │  3. 按 Priority 排序，选择最高优先级的策略                           │   │
│  │  4. 若无匹配，使用内置默认策略                                       │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      │ 检查项列表
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  CheckRunner (执行引擎)                                                      │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  1. 按 Mode 分组（Parallel / Sequential）                           │   │
│  │  2. 评估 Condition 条件表达式                                       │   │
│  │  3. 并行/串行执行检查项                                             │   │
│  │  4. 支持超时控制和重试                                              │   │
│  │  5. 生成 CheckReport                                                │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 与 UpgradePath 的职责分离

| 资源 | 职责 | 管理团队 | 存储方式 |
|------|------|---------|---------|
| **UpgradePath** | 定义版本转换合法性（from/to/blocked） | 平台团队 | OCI 镜像 |
| **CheckPolicy** | 定义升级前后的检查策略 | 集群运维 | K8s CRD |
| **ReleaseImage** | 定义版本包含的组件清单 | 平台团队 | OCI 镜像 |

三者完全独立，互不耦合：
- UpgradePath 变更（新增版本路径）不影响检查策略
- CheckPolicy 变更（调整检查项）不影响版本路径
- 不同环境（生产/测试）可使用不同的 CheckPolicy，共享相同的 UpgradePath

### 4.3 设计优势

| 维度 | 说明 |
|------|------|
| **职责分离** | UpgradePath 只管路径合法性，CheckPolicy 只管检查策略 |
| **配置复用** | 全局配置一次，所有升级路径共享 |
| **灵活性** | 支持按集群标签/环境定制不同检查策略 |
| **扩展性** | 新增检查项只需修改 CheckPolicy，无需触碰升级路径 |
| **独立管理** | 平台团队管理 UpgradePath，运维团队管理 CheckPolicy |

## 5. 数据结构设计

### 5.1 CheckPolicy CRD

```go
// api/cvo/v1beta1/checkpolicy_types.go

// CheckPolicy 检查策略 CRD
type CheckPolicy struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    
    Spec   CheckPolicySpec   `json:"spec,omitempty"`
    Status CheckPolicyStatus `json:"status,omitempty"`
}

// CheckPolicySpec 检查策略规格
type CheckPolicySpec struct {
    // 预检策略（升级前执行）
    PreCheck []CheckStep `json:"preCheck,omitempty"`
    
    // 后检策略（升级后执行）
    PostCheck []CheckStep `json:"postCheck,omitempty"`
    
    // 选择器（指定此策略适用的集群）
    // 空选择器表示适用于所有集群
    Selector *metav1.LabelSelector `json:"selector,omitempty"`
    
    // 优先级（多个 CheckPolicy 匹配时，优先级高的生效）
    // 默认 0，数值越大优先级越高
    Priority int `json:"priority,omitempty"`
}

// CheckStep 检查步骤定义
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
    
    // 执行条件（Go template 表达式，为空则始终执行）
    // 示例: "{{.Cluster.NodeCount}} > 10"
    Condition string `json:"condition,omitempty"`
}

// CheckPolicyStatus 检查策略状态
type CheckPolicyStatus struct {
    // 策略状态
    Phase CheckPolicyPhase `json:"phase,omitempty"`
    
    // 最后验证时间
    LastValidatedAt *metav1.Time `json:"lastValidatedAt,omitempty"`
    
    // 验证消息
    Message string `json:"message,omitempty"`
}

type CheckPolicyPhase string

const (
    CheckPolicyPhaseValid   CheckPolicyPhase = "Valid"
    CheckPolicyPhaseInvalid CheckPolicyPhase = "Invalid"
)
```

### 5.2 CheckResult 和 CheckReport

```go
// pkg/check/types.go

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
    Name       string                 `json:"name"`
    Status     CheckStatus            `json:"status"`
    Message    string                 `json:"message,omitempty"`
    Duration   string                 `json:"duration,omitempty"`
    Error      string                 `json:"error,omitempty"`
    Details    map[string]interface{} `json:"details,omitempty"`
    ExecutedAt metav1.Time            `json:"executedAt,omitempty"`
}

// CheckReport 检查报告
type CheckReport struct {
    Status         CheckStatus   `json:"status"`
    Results        []CheckResult `json:"results,omitempty"`
    TotalDuration  string        `json:"totalDuration,omitempty"`
    PassedCount    int           `json:"passedCount"`
    FailedCount    int           `json:"failedCount"`
    SkippedCount   int           `json:"skippedCount"`
    PolicyName     string        `json:"policyName,omitempty"`     // 使用的 CheckPolicy 名称
    ExecutedAt     metav1.Time   `json:"executedAt,omitempty"`
}

// ClusterVersionStatus 扩展
type ClusterVersionStatus struct {
    // ... 现有字段 ...
    
    PreCheckReport  *CheckReport `json:"preCheckReport,omitempty"`
    PostCheckReport *CheckReport `json:"postCheckReport,omitempty"`
}
```

### 5.3 CheckItem 接口

```go
// pkg/check/item.go

// CheckItem 检查项接口
type CheckItem interface {
    // Name 返回检查项名称
    Name() string
    
    // Execute 执行检查
    Execute(ctx context.Context, cluster *bkev1beta1.BKECluster, params map[string]string) (*CheckResult, error)
    
    // Description 返回检查项描述
    Description() string
    
    // Phase 返回检查项适用阶段（pre/post/both）
    Phase() CheckPhase
}

type CheckPhase string

const (
    CheckPhasePre  CheckPhase = "pre"
    CheckPhasePost CheckPhase = "post"
    CheckPhaseBoth CheckPhase = "both"
)
```

## 6. 检查执行引擎

### 6.1 CheckRegistry 注册表

```go
// pkg/check/registry.go

type CheckRegistry struct {
    mu       sync.RWMutex
    registry map[string]CheckItem
}

func NewCheckRegistry() *CheckRegistry {
    return &CheckRegistry{
        registry: make(map[string]CheckItem),
    }
}

func (r *CheckRegistry) Register(item CheckItem) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.registry[item.Name()] = item
}

func (r *CheckRegistry) Get(name string) (CheckItem, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    item, ok := r.registry[name]
    return item, ok
}
```

### 6.2 CheckPolicyResolver 策略解析器

```go
// pkg/check/resolver.go

// CheckPolicyResolver 解析检查策略
type CheckPolicyResolver struct {
    client client.Client
}

// ResolvePreChecks 解析预检策略
func (r *CheckPolicyResolver) ResolvePreChecks(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) ([]cvoapi.CheckStep, string, error) {
    policy, err := r.getMatchingCheckPolicy(ctx, cluster)
    if err != nil {
        return nil, "", fmt.Errorf("failed to resolve CheckPolicy: %w", err)
    }
    return policy.Spec.PreCheck, policy.Name, nil
}

// ResolvePostChecks 解析后检策略
func (r *CheckPolicyResolver) ResolvePostChecks(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) ([]cvoapi.CheckStep, string, error) {
    policy, err := r.getMatchingCheckPolicy(ctx, cluster)
    if err != nil {
        return nil, "", fmt.Errorf("failed to resolve CheckPolicy: %w", err)
    }
    return policy.Spec.PostCheck, policy.Name, nil
}

// getMatchingCheckPolicy 按标签匹配 + 优先级选择 CheckPolicy
func (r *CheckPolicyResolver) getMatchingCheckPolicy(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*cvoapi.CheckPolicy, error) {
    // 查找所有 CheckPolicy
    policyList := &cvoapi.CheckPolicyList{}
    if err := r.client.List(ctx, policyList); err != nil {
        return nil, err
    }
    
    // 筛选匹配的 CheckPolicy
    var matchedPolicies []*cvoapi.CheckPolicy
    for i := range policyList.Items {
        policy := &policyList.Items[i]
        if matchesSelector(policy.Spec.Selector, cluster) {
            matchedPolicies = append(matchedPolicies, policy)
        }
    }
    
    if len(matchedPolicies) == 0 {
        // 无匹配，返回内置默认策略
        return r.getDefaultCheckPolicy(), nil
    }
    
    // 按优先级排序，返回最高优先级的
    sort.Slice(matchedPolicies, func(i, j int) bool {
        return matchedPolicies[i].Spec.Priority > matchedPolicies[j].Spec.Priority
    })
    
    return matchedPolicies[0], nil
}

// matchesSelector 检查集群是否匹配选择器
func matchesSelector(selector *metav1.LabelSelector, cluster *bkev1beta1.BKECluster) bool {
    if selector == nil {
        return true // 空选择器匹配所有集群
    }
    
    // 转换 selector
    sel, err := metav1.LabelSelectorAsSelector(selector)
    if err != nil {
        return false
    }
    
    // 检查集群标签是否匹配
    return sel.Matches(labels.Set(cluster.Labels))
}

// getDefaultCheckPolicy 返回内置默认 CheckPolicy
func (r *CheckPolicyResolver) getDefaultCheckPolicy() *cvoapi.CheckPolicy {
    return &cvoapi.CheckPolicy{
        ObjectMeta: metav1.ObjectMeta{Name: "default"},
        Spec: cvoapi.CheckPolicySpec{
            PreCheck: []cvoapi.CheckStep{
                {Name: "cluster-health", Required: true, Timeout: "30s"},
                {Name: "backup-verification", Required: true, Timeout: "5m"},
                {Name: "resource-check", Required: false, Timeout: "1m"},
                {Name: "component-dependency", Required: true, Timeout: "30s"},
            },
            PostCheck: []cvoapi.CheckStep{
                {Name: "component-version", Required: true, Timeout: "30s"},
                {Name: "cluster-health", Required: true, Timeout: "1m"},
                {Name: "node-ready", Required: true, Timeout: "30s"},
            },
        },
    }
}
```

### 6.3 CheckRunner 执行器

```go
// pkg/check/runner.go

type CheckRunner struct {
    registry *CheckRegistry
    resolver *CheckPolicyResolver
    logger   logr.Logger
}

// RunPreChecks 执行预检
func (r *CheckRunner) RunPreChecks(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*CheckReport, error) {
    // 解析策略
    checks, policyName, err := r.resolver.ResolvePreChecks(ctx, cluster)
    if err != nil {
        return nil, err
    }
    
    r.logger.Info("Running pre-checks",
        "policyName", policyName,
        "checkCount", len(checks),
    )
    
    report, err := r.runChecks(ctx, cluster, checks, "pre-check")
    if err != nil {
        return nil, err
    }
    
    report.PolicyName = policyName
    return report, nil
}

// RunPostChecks 执行后检
func (r *CheckRunner) RunPostChecks(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
) (*CheckReport, error) {
    // 解析策略
    checks, policyName, err := r.resolver.ResolvePostChecks(ctx, cluster)
    if err != nil {
        return nil, err
    }
    
    r.logger.Info("Running post-checks",
        "policyName", policyName,
        "checkCount", len(checks),
    )
    
    report, err := r.runChecks(ctx, cluster, checks, "post-check")
    if err != nil {
        return nil, err
    }
    
    report.PolicyName = policyName
    return report, nil
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
        // 评估条件
        if step.Condition != "" {
            if !evaluateCondition(step.Condition, cluster) {
                report.Results = append(report.Results, cvoapi.CheckResult{
                    Name:       step.Name,
                    Status:     cvoapi.CheckStatusSkipped,
                    Message:    "Condition not met",
                    ExecutedAt: metav1.Now(),
                })
                report.SkippedCount++
                continue
            }
        }
        
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
            for _, step := range steps {
                if step.Name == result.Name && step.Required {
                    report.Status = cvoapi.CheckStatusFailed
                    break
                }
            }
        } else if result.Status == cvoapi.CheckStatusPassed {
            report.PassedCount++
        }
    }
    
    report.TotalDuration = time.Since(startTime).String()
    
    r.logger.Info("Check completed",
        "phase", phase,
        "status", report.Status,
        "passed", report.PassedCount,
        "failed", report.FailedCount,
        "skipped", report.SkippedCount,
        "duration", report.TotalDuration,
    )
    
    return report, nil
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
    for attempt := 0; attempt <= maxRetries; attempt++ {
        if attempt > 0 {
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
            Name:    step.Name,
            Status:  cvoapi.CheckStatusFailed,
            Message: "Check execution failed",
        }
    }
    
    // 检查是否超时
    if checkCtx.Err() == context.DeadlineExceeded {
        result.Status = cvoapi.CheckStatusTimeout
        result.Error = "Check execution timeout"
    }
    
    result.Duration = time.Since(startTime).String()
    result.ExecutedAt = metav1.Now()
    
    if lastErr != nil && result.Error == "" {
        result.Error = lastErr.Error()
    }
    
    return result, nil
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
    
    for _, err := range errors {
        if err != nil {
            return nil, err
        }
    }
    
    return results, nil
}
```

## 7. 预检项设计

### 7.1 预检项清单

| 检查项 | 名称 | 说明 | 默认 Required | 默认 Timeout | Phase |
|--------|------|------|---------------|--------------|-------|
| 集群健康检查 | `cluster-health` | 检查 etcd 成员状态、API Server 可用性、节点 Ready 比例 | true | 30s | both |
| 备份验证 | `backup-verification` | 验证 etcd 快照可用性、配置备份完整性 | true | 5m | pre |
| 资源检查 | `resource-check` | 检查磁盘空间、内存/CPU 余量、镜像预拉取 | false | 1m | pre |
| 组件依赖检查 | `component-dependency` | 验证 ComponentVersion 兼容性约束 | true | 30s | pre |

### 7.2 ClusterHealthCheckItem 实现

```go
// pkg/check/items/cluster_health.go

type ClusterHealthCheckItem struct {
    client client.Client
}

func (c *ClusterHealthCheckItem) Name() string        { return "cluster-health" }
func (c *ClusterHealthCheckItem) Description() string { return "Check cluster health: etcd, API server, nodes" }
func (c *ClusterHealthCheckItem) Phase() CheckPhase   { return CheckPhaseBoth }

func (c *ClusterHealthCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*CheckResult, error) {
    result := &CheckResult{
        Name:       c.Name(),
        Status:     CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    // 1. 检查 etcd Pod 状态
    etcdPods := &corev1.PodList{}
    if err := c.client.List(ctx, etcdPods,
        client.InNamespace("kube-system"),
        client.MatchingLabels{"component": "etcd"},
    ); err != nil {
        result.Status = CheckStatusFailed
        result.Error = fmt.Sprintf("failed to list etcd pods: %v", err)
        return result, nil
    }
    
    etcdHealthy := true
    for _, pod := range etcdPods.Items {
        if pod.Status.Phase != corev1.PodRunning {
            etcdHealthy = false
            break
        }
    }
    result.Details["etcd"] = map[string]interface{}{
        "healthy": etcdHealthy,
        "count":   len(etcdPods.Items),
    }
    
    // 2. 检查 API Server Pod 状态
    apiPods := &corev1.PodList{}
    if err := c.client.List(ctx, apiPods,
        client.InNamespace("kube-system"),
        client.MatchingLabels{"component": "kube-apiserver"},
    ); err != nil {
        result.Status = CheckStatusFailed
        result.Error = fmt.Sprintf("failed to list apiserver pods: %v", err)
        return result, nil
    }
    
    apiHealthy := true
    for _, pod := range apiPods.Items {
        if pod.Status.Phase != corev1.PodRunning {
            apiHealthy = false
            break
        }
    }
    result.Details["apiServer"] = map[string]interface{}{
        "healthy": apiHealthy,
        "count":   len(apiPods.Items),
    }
    
    // 3. 检查节点 Ready 比例
    nodeList := &corev1.NodeList{}
    if err := c.client.List(ctx, nodeList); err != nil {
        result.Status = CheckStatusFailed
        result.Error = fmt.Sprintf("failed to list nodes: %v", err)
        return result, nil
    }
    
    readyCount := 0
    for _, node := range nodeList.Items {
        for _, cond := range node.Status.Conditions {
            if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
                readyCount++
                break
            }
        }
    }
    
    readyRatio := float64(readyCount) / float64(len(nodeList.Items))
    nodeHealthy := readyRatio >= 0.8
    
    result.Details["nodes"] = map[string]interface{}{
        "healthy":    nodeHealthy,
        "readyCount": readyCount,
        "totalCount": len(nodeList.Items),
        "readyRatio": readyRatio,
    }
    
    // 综合判断
    if !etcdHealthy || !apiHealthy || !nodeHealthy {
        result.Status = CheckStatusFailed
        result.Message = "Cluster health check failed"
    } else {
        result.Message = fmt.Sprintf("Cluster is healthy: %d/%d nodes ready", readyCount, len(nodeList.Items))
    }
    
    return result, nil
}
```

### 7.3 BackupVerificationCheckItem 实现

```go
// pkg/check/items/backup_verification.go

type BackupVerificationCheckItem struct {
    client client.Client
}

func (b *BackupVerificationCheckItem) Name() string        { return "backup-verification" }
func (b *BackupVerificationCheckItem) Description() string { return "Verify etcd backup availability" }
func (b *BackupVerificationCheckItem) Phase() CheckPhase   { return CheckPhasePre }

func (b *BackupVerificationCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*CheckResult, error) {
    result := &CheckResult{
        Name:       b.Name(),
        Status:     CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    // 检查最近的 etcd 备份
    backupList := &bkev1beta1.EtcdBackupList{}
    if err := b.client.List(ctx, backupList, client.InNamespace(cluster.Namespace)); err != nil {
        result.Status = CheckStatusFailed
        result.Error = fmt.Sprintf("failed to list etcd backups: %v", err)
        return result, nil
    }
    
    if len(backupList.Items) == 0 {
        result.Status = CheckStatusFailed
        result.Message = "No etcd backups found"
        return result, nil
    }
    
    // 找到最近的备份
    var latest *bkev1beta1.EtcdBackup
    for i := range backupList.Items {
        backup := &backupList.Items[i]
        if latest == nil || backup.CreationTimestamp.After(latest.CreationTimestamp.Time) {
            latest = backup
        }
    }
    
    // 检查备份是否在 24 小时内
    backupAge := time.Since(latest.CreationTimestamp.Time)
    if backupAge > 24*time.Hour {
        result.Status = CheckStatusFailed
        result.Message = fmt.Sprintf("Latest backup is too old: %v", backupAge.Round(time.Hour))
        return result, nil
    }
    
    // 检查备份状态
    if latest.Status.Phase != bkev1beta1.BackupPhaseCompleted {
        result.Status = CheckStatusFailed
        result.Message = fmt.Sprintf("Latest backup is not completed: %s", latest.Status.Phase)
        return result, nil
    }
    
    result.Details["latestBackup"] = map[string]interface{}{
        "name":      latest.Name,
        "createdAt": latest.CreationTimestamp.Time,
        "age":       backupAge.String(),
        "status":    latest.Status.Phase,
    }
    
    result.Message = fmt.Sprintf("Latest backup is valid (age: %v)", backupAge.Round(time.Minute))
    return result, nil
}
```

## 8. 后检项设计

### 8.1 后检项清单

| 检查项 | 名称 | 说明 | 默认 Required | 默认 Timeout | Phase |
|--------|------|------|---------------|--------------|-------|
| 组件版本验证 | `component-version` | 验证所有组件版本一致性 | true | 30s | post |
| 集群健康验证 | `cluster-health` | 验证 etcd/apiserver/controller-manager/scheduler 状态 | true | 1m | both |
| 节点状态验证 | `node-ready` | 验证所有节点 Ready | true | 30s | post |
| 业务应用验证 | `application-health` | 验证关键 Deployment/DaemonSet 就绪 | false | 2m | post |

### 8.2 ComponentVersionCheckItem 实现

```go
// pkg/check/items/component_version.go

type ComponentVersionCheckItem struct {
    client        client.Client
    manifestStore *manifest.ManifestStore
}

func (c *ComponentVersionCheckItem) Name() string        { return "component-version" }
func (c *ComponentVersionCheckItem) Description() string { return "Verify all components are at target version" }
func (c *ComponentVersionCheckItem) Phase() CheckPhase   { return CheckPhasePost }

func (c *ComponentVersionCheckItem) Execute(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    params map[string]string,
) (*CheckResult, error) {
    result := &CheckResult{
        Name:       c.Name(),
        Status:     CheckStatusPassed,
        ExecutedAt: metav1.Now(),
        Details:    make(map[string]interface{}),
    }
    
    targetVersion := params["targetVersion"]
    if targetVersion == "" {
        result.Status = CheckStatusFailed
        result.Message = "targetVersion is required"
        return result, nil
    }
    
    // 获取目标版本的 ReleaseImage
    targetRI, err := c.manifestStore.GetReleaseImage(targetVersion)
    if err != nil {
        result.Status = CheckStatusFailed
        result.Error = fmt.Sprintf("failed to get ReleaseImage for %s: %v", targetVersion, err)
        return result, nil
    }
    
    // 检查每个组件的当前版本
    mismatches := make([]map[string]string, 0)
    
    for _, comp := range targetRI.Spec.Install.Components {
        currentVersion := c.getComponentCurrentVersion(ctx, cluster, comp.Name)
        if currentVersion != comp.Version {
            mismatches = append(mismatches, map[string]string{
                "component":      comp.Name,
                "targetVersion":  comp.Version,
                "currentVersion": currentVersion,
            })
        }
    }
    
    if len(mismatches) > 0 {
        result.Status = CheckStatusFailed
        result.Message = fmt.Sprintf("%d components are not at target version", len(mismatches))
        result.Details["mismatches"] = mismatches
    } else {
        result.Message = "All components are at target version"
    }
    
    return result, nil
}

func (c *ComponentVersionCheckItem) getComponentCurrentVersion(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    componentName string,
) string {
    // 从 ClusterVersion.Status.ComponentVersions 获取
    if cluster.Status.ComponentVersions != nil {
        if ver, ok := cluster.Status.ComponentVersions[componentName]; ok {
            return ver
        }
    }
    return ""
}
```

## 9. YAML 示例

### 9.1 全局 CheckPolicy

```yaml
# 全局检查策略（适用于所有集群）
apiVersion: cvo.openfuyao.cn/v1beta1
kind: CheckPolicy
metadata:
  name: default
spec:
  preCheck:
    - name: cluster-health
      required: true
      timeout: "30s"
    - name: backup-verification
      required: true
      timeout: "5m"
    - name: resource-check
      required: false
      timeout: "1m"
    - name: component-dependency
      required: true
      timeout: "30s"
  postCheck:
    - name: component-version
      required: true
      timeout: "30s"
    - name: cluster-health
      required: true
      timeout: "1m"
    - name: node-ready
      required: true
      timeout: "30s"
    - name: application-health
      required: false
      timeout: "2m"
```

### 9.2 环境特定 CheckPolicy

```yaml
# 生产环境检查策略（更严格）
apiVersion: cvo.openfuyao.cn/v1beta1
kind: CheckPolicy
metadata:
  name: production
spec:
  selector:
    matchLabels:
      environment: production
  priority: 10  # 高于 default (priority=0)
  preCheck:
    - name: cluster-health
      required: true
      timeout: "30s"
    - name: backup-verification
      required: true
      timeout: "5m"
      params:
        maxAge: "12h"  # 生产环境要求备份在 12 小时内
    - name: resource-check
      required: true   # 生产环境资源检查为必须
      timeout: "1m"
      params:
        minDiskPercent: "30"
        minMemoryPercent: "20"
    - name: component-dependency
      required: true
      timeout: "30s"
  postCheck:
    - name: component-version
      required: true
      timeout: "30s"
    - name: cluster-health
      required: true
      timeout: "1m"
    - name: node-ready
      required: true
      timeout: "30s"
      params:
        minReadyRatio: "1.0"  # 生产环境要求 100% 节点就绪
    - name: application-health
      required: true          # 生产环境应用检查为必须
      timeout: "5m"
```

## 10. 检查结果报告

### 10.1 CheckReport 示例

```yaml
# ClusterVersion Status 中的检查报告
status:
  phase: "Upgrading"
  preCheckReport:
    status: "Passed"
    policyName: "production"           # 使用的 CheckPolicy
    results:
      - name: cluster-health
        status: Passed
        message: "Cluster is healthy: 66/66 nodes ready"
        duration: "2.5s"
      - name: backup-verification
        status: Passed
        message: "Latest backup is valid (age: 2h)"
        duration: "45s"
      - name: resource-check
        status: Passed
        message: "Resource check passed"
        duration: "15s"
      - name: component-dependency
        status: Passed
        message: "All 15 components passed compatibility check"
        duration: "3s"
    totalDuration: "65.5s"
    passedCount: 4
    failedCount: 0
    skippedCount: 0
    executedAt: "2026-08-20T10:00:00Z"
```

### 10.2 Event 事件记录

```go
func (r *ClusterVersionReconciler) recordCheckEvent(
    cv *cvoapi.ClusterVersion,
    phase string,
    report *CheckReport,
) {
    eventType := v1.EventTypeNormal
    reason := "CheckPassed"
    
    if report.Status == CheckStatusFailed {
        eventType = v1.EventTypeWarning
        reason = "CheckFailed"
    }
    
    r.Recorder.Eventf(
        cv,
        eventType,
        reason,
        "%s completed (policy: %s): %d passed, %d failed, %d skipped (duration: %s)",
        phase,
        report.PolicyName,
        report.PassedCount,
        report.FailedCount,
        report.SkippedCount,
        report.TotalDuration,
    )
}
```

## 11. 可观测性

### 11.1 Prometheus 指标

```go
var (
    checkExecutionTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "bke_check_execution_total",
            Help: "Total number of check executions",
        },
        []string{"check_name", "phase", "status", "policy_name"},
    )
    
    checkDurationSeconds = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "bke_check_duration_seconds",
            Help:    "Duration of check execution in seconds",
            Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
        },
        []string{"check_name", "phase"},
    )
    
    checkPolicyResolveTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "bke_check_policy_resolve_total",
            Help: "Total number of CheckPolicy resolve operations",
        },
        []string{"policy_name"},
    )
)
```

## 12. 与现有系统集成

### 12.1 ClusterVersionReconciler 集成

```go
func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &cvoapi.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // ... 现有逻辑 ...
    
    // Step 4: 执行预检（CheckPolicyResolver 自动按集群标签匹配策略）
    preCheckReport, err := r.checkRunner.RunPreChecks(ctx, cluster)
    if err != nil {
        return r.updateStatus(ctx, cv, cvoapi.PhaseFailed, fmt.Sprintf("pre-check error: %v", err))
    }
    
    cv.Status.PreCheckReport = preCheckReport
    r.recordCheckEvent(cv, "PreCheck", preCheckReport)
    
    if preCheckReport.Status == CheckStatusFailed {
        return r.updateStatus(ctx, cv, cvoapi.PhasePreCheckFailed, "pre-check failed")
    }
    
    // Step 5: 执行 DAG
    // ... 现有逻辑 ...
    
    // Step 6: 执行后检
    postCheckReport, err := r.checkRunner.RunPostChecks(ctx, cluster)
    if err != nil {
        r.Log.Error(err, "post-check failed")
        r.Recorder.Eventf(cv, v1.EventTypeWarning, "PostCheckFailed", "post-check error: %v", err)
    } else {
        cv.Status.PostCheckReport = postCheckReport
        r.recordCheckEvent(cv, "PostCheck", postCheckReport)
    }
    
    return r.updateStatus(ctx, cv, cvoapi.PhaseReady, "")
}
```

### 12.2 检查项注册

```go
func main() {
    // ... 初始化控制器 ...
    
    checkRegistry := check.NewCheckRegistry()
    
    // 注册检查项
    checkRegistry.Register(&items.ClusterHealthCheckItem{Client: mgr.GetClient()})
    checkRegistry.Register(&items.BackupVerificationCheckItem{Client: mgr.GetClient()})
    checkRegistry.Register(&items.ResourceCheckItem{Client: mgr.GetClient()})
    checkRegistry.Register(&items.ComponentDependencyCheckItem{ManifestStore: manifestStore})
    checkRegistry.Register(&items.ComponentVersionCheckItem{Client: mgr.GetClient(), ManifestStore: manifestStore})
    checkRegistry.Register(&items.NodeReadyCheckItem{Client: mgr.GetClient()})
    checkRegistry.Register(&items.ApplicationHealthCheckItem{Client: mgr.GetClient()})
    
    // 创建策略解析器和执行器
    resolver := check.NewCheckPolicyResolver(mgr.GetClient())
    checkRunner := check.NewCheckRunner(checkRegistry, resolver, mgr.GetLogger())
    
    // 注入到 ClusterVersionReconciler
    cvReconciler := &ClusterVersionReconciler{
        Client:           mgr.GetClient(),
        checkRunner:      checkRunner,
        upgradePathGraph: upgradePathGraph,
        manifestStore:    manifestStore,
    }
    
    // ... 启动控制器 ...
}
```

## 13. 测试设计

### 13.1 单元测试

| 测试场景 | 测试内容 | 预期结果 |
|---------|---------|---------|
| CheckRegistry 注册 | 注册检查项并获取 | 正确返回检查项 |
| CheckRunner 执行 | 执行单个检查项 | 返回正确的 CheckResult |
| 并行执行 | 并行执行多个检查项 | 所有检查项并行完成 |
| 超时控制 | 检查项执行超时 | 返回 Timeout 状态 |
| 重试机制 | 检查项失败后重试 | 按指定次数重试 |
| Required 判断 | Required=true 失败 | 整体状态为 Failed |
| Required 判断 | Required=false 失败 | 整体状态为 Passed |
| CheckPolicyResolver - 默认策略 | 无 CheckPolicy 资源 | 返回内置默认策略 |
| CheckPolicyResolver - 标签匹配 | 集群标签匹配 CheckPolicy | 返回匹配的 CheckPolicy |
| CheckPolicyResolver - 优先级 | 多个 CheckPolicy 匹配 | 返回最高优先级的 |

### 13.2 集成测试

| 测试场景 | 测试内容 | 预期结果 |
|---------|---------|---------|
| 全局策略执行 | 仅配置全局 CheckPolicy | 使用全局策略执行检查 |
| 环境差异化 | 生产/测试环境不同策略 | 按环境选择正确策略 |
| 预检失败阻断 | Required=true 预检失败 | 升级被阻断 |
| 预检告警 | Required=false 预检失败 | 升级继续，记录告警 |

## 14. 工作量评估

| 阶段 | 任务内容 | 工作量 (人天) |
|------|---------|-------------|
| CheckPolicy CRD | CRD 定义、Webhook 验证 | 2 |
| CheckPolicyResolver | 策略解析、标签匹配、优先级选择 | 2 |
| 检查框架 | CheckRegistry、CheckRunner | 3 |
| 预检项开发 | 4 个预检项实现 | 6 |
| 后检项开发 | 4 个后检项实现 | 5 |
| 集成开发 | ClusterVersionReconciler 集成 | 3 |
| 可观测性 | Prometheus 指标 | 2 |
| 测试 | 单元测试、集成测试 | 4 |
| **总计** | | **27 人天** |

## 15. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| CheckPolicy 配置错误 | 检查项缺失或错误 | Webhook 验证 CheckPolicy 合法性，启动时校验 |
| 检查项执行超时 | 升级流程阻塞 | 设置合理超时时间，支持跳过非关键检查 |
| 检查项误报 | 升级被错误阻断 | 支持 Required=false 告警模式，支持手动跳过 |
| 检查项漏报 | 升级后发现问题 | 后检项覆盖关键组件，支持手动触发重新检查 |
| 多个 CheckPolicy 冲突 | 选择了错误的策略 | 日志记录策略选择过程，支持 dry-run 预览 |

## 16. 部署步骤

1. **部署 CheckPolicy CRD**：注册 CheckPolicy CRD 到管理集群
2. **创建默认 CheckPolicy**：创建 `name: default` 的全局 CheckPolicy
3. **注册检查项**：在控制器启动时注册所有内置检查项
4. **创建环境策略**（可选）：为生产/测试环境创建差异化 CheckPolicy
5. **启用检查框架**：通过 Feature Gate 启用预检/后检功能

---

**文档版本**: v2.1  
**维护者**: openFuyao Team

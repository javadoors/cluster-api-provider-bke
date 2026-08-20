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
| **替代** | kep5-2-precheck-postcheck-design.md (v1) |

## 1. 摘要

本文档是 KEP-5 声明式升级框架的补充设计，定义升级前预检（Pre-Check）与升级后检查（Post-Check）的完整架构。通过引入独立的 `CheckPolicy` CRD，将检查策略与升级路径解耦，实现更灵活、可维护的检查框架。

## 2. 动机

### 2.1 v1 设计问题分析

v1 设计将预检/后检绑定到 `UpgradePathEdge`：

```yaml
# v1 设计：预检绑定到 UpgradePathEdge
spec.paths:
  - from: "v2.5.0"
    to: "v2.6.0"
    preCheck:                    # ← 问题：每条路径都要重复配置
      - name: "cluster-health"
        required: true
      - name: "backup-verification"
        required: true
    postCheck:                   # ← 问题：每条路径都要重复配置
      - name: "component-version"
        required: true
```

**问题分析**：

| 问题 | 说明 | 严重程度 | 影响 |
|------|------|---------|------|
| **职责混淆** | UpgradePath 定义"哪些版本转换合法"，预检定义"升级前需满足什么条件"，两者职责不同 | 高 | 违反单一职责原则 |
| **维护成本高** | 100 条升级路径需重复配置相同的预检项（cluster-health、backup-verification 等） | 高 | 配置冗余，易出错 |
| **关注点分离不清** | UpgradePath 由平台团队管理（OCI 镜像），预检由集群运维管理 | 中 | 管理权限混乱 |
| **灵活性不足** | 无法按集群/环境定制预检策略（如生产环境需要备份检查，测试环境不需要） | 中 | 无法差异化配置 |
| **扩展困难** | 新增预检项需修改所有 UpgradePathEdge | 高 | 升级路径镜像需频繁更新 |

### 2.2 目标

1. **解耦设计**：将检查策略从 UpgradePath 中解耦，引入独立的 `CheckPolicy` CRD
2. **分层策略**：支持全局默认策略 + 路径特定覆盖
3. **灵活配置**：支持按集群/环境定制检查策略
4. **向后兼容**：保留 UpgradePathEdge 的 preCheck/postCheck 字段作为覆盖机制

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
| 策略合并机制 | 全局策略 + 路径覆盖的合并逻辑 |
| 预检项设计 | 集群健康、备份验证、资源检查、依赖检查 |
| 后检项设计 | 版本验证、健康验证、节点验证、应用验证 |
| 执行引擎 | 并行/串行执行、超时控制、失败策略 |
| 结果报告 | CheckReport 聚合、持久化、事件记录 |

### 3.2 约束

| 约束 | 说明 |
|------|------|
| **向后兼容** | 保留 UpgradePathEdge 的 preCheck/postCheck 字段 |
| **插件化** | 检查项通过注册机制接入，不侵入核心控制器 |
| **幂等性** | 检查项执行必须幂等，可重复执行 |
| **超时控制** | 单个检查项超时不应阻塞整体升级流程 |

## 4. 架构设计

### 4.1 整体架构

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          检查框架整体架构 v2                                  │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│  CheckPolicy CRD (独立资源)                     ◀──── v2 新增               │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  全局 CheckPolicy (集群级别)                                         │   │
│  │  ┌──────────────────────────────────────────────────────────────┐  │   │
│  │  │  name: "default"                                              │  │   │
│  │  │  spec:                                                        │  │   │
│  │  │    preCheck:         ← 全局预检策略                           │  │   │
│  │  │      - name: "cluster-health"                                 │  │   │
│  │  │        required: true                                         │  │   │
│  │  │      - name: "backup-verification"                            │  │   │
│  │  │        required: true                                         │  │   │
│  │  │      - name: "resource-check"                                 │  │   │
│  │  │        required: false                                        │  │   │
│  │  │    postCheck:        ← 全局后检策略                           │  │   │
│  │  │      - name: "component-version"                              │  │   │
│  │  │        required: true                                         │  │   │
│  │  │      - name: "cluster-health"                                 │  │   │
│  │  │        required: true                                         │  │   │
│  │  └──────────────────────────────────────────────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      │ 合并
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  UpgradePath CRD (路径特定覆盖)                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  spec.paths:                                                         │   │
│  │    - from: "v2.5.0"                                                  │   │
│  │      to: "v2.6.0"                                                    │   │
│  │      preCheckOverride:        ◀──── 仅覆盖/追加，非完整定义          │   │
│  │        override: "replace"    # replace / merge / append             │   │
│  │        checks:                                                       │   │
│  │          - name: "etcd-version-check"   # 路径特定检查               │   │
│  │            required: true                                            │   │
│  │      postCheckOverride:       ◀──── 仅覆盖/追加                      │   │
│  │        override: "append"                                            │   │
│  │        checks:                                                       │   │
│  │          - name: "data-migration-check"                              │   │
│  │            required: true                                            │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      │ 合并结果
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  CheckRunner (执行引擎)                                                      │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │  合并策略:                                                           │   │
│  │  ┌──────────────────────────────────────────────────────────────┐  │   │
│  │  │  1. 加载全局 CheckPolicy                                     │  │   │
│  │  │  2. 查找匹配的 UpgradePathEdge                               │  │   │
│  │  │  3. 根据 override 策略合并:                                  │  │   │
│  │  │     - replace: 完全替换全局策略                              │  │   │
│  │  │     - merge: 按 name 合并（路径覆盖全局）                    │  │   │
│  │  │     - append: 追加到全局策略后                               │  │   │
│  │  │  4. 执行合并后的检查项列表                                   │  │   │
│  │  └──────────────────────────────────────────────────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 4.2 策略合并机制

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          策略合并流程                                         │
└─────────────────────────────────────────────────────────────────────────────┘

全局 CheckPolicy:
  preCheck: [cluster-health, backup-verification, resource-check]

UpgradePathEdge (v2.5.0 → v2.6.0):
  preCheckOverride:
    override: "merge"
    checks:
      - name: "etcd-version-check"
        required: true
      - name: "cluster-health"        # 覆盖全局的 cluster-health
        required: false               # 改为非必须

合并结果:
  preCheck: [etcd-version-check, cluster-health(required=false), 
             backup-verification, resource-check]

┌─────────────────────────────────────────────────────────────────────────────┐
│                          override 策略说明                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  replace: 完全替换全局策略                                                   │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │  全局: [A, B, C]                                                     │   │
│  │  路径: override=replace, checks=[X, Y]                               │   │
│  │  结果: [X, Y]                                                        │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  merge: 按 name 合并，路径覆盖全局同名项                                     │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │  全局: [A, B, C]                                                     │   │
│  │  路径: override=merge, checks=[B', X]                                │   │
│  │  结果: [A, B', C, X]  (B 被 B' 覆盖，X 追加)                        │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  append: 追加到全局策略后                                                    │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │  全局: [A, B, C]                                                     │   │
│  │  路径: override=append, checks=[X, Y]                                │   │
│  │  结果: [A, B, C, X, Y]                                               │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 4.3 与 v1 设计对比

| 维度 | v1 设计 | v2 设计 |
|------|---------|---------|
| **检查策略定义** | 绑定到 UpgradePathEdge | 独立 CheckPolicy CRD |
| **配置复用** | 每条路径重复配置 | 全局配置一次，路径可选覆盖 |
| **职责分离** | UpgradePath 承担路径+检查双重职责 | UpgradePath 只管路径，CheckPolicy 管检查 |
| **灵活性** | 无法按集群定制 | 支持按集群/环境定制 |
| **扩展性** | 新增检查项需修改所有路径 | 新增检查项只需修改 CheckPolicy |
| **向后兼容** | N/A | 保留 UpgradePathEdge.preCheck/postCheck 作为覆盖 |

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

### 5.2 UpgradePathEdge 扩展（覆盖机制）

```go
// api/cvo/v1beta1/upgradepath_types.go

// UpgradePathEdge 升级路径边（扩展）
type UpgradePathEdge struct {
    From       string `json:"from"`
    To         string `json:"to"`
    Blocked    bool   `json:"blocked,omitempty"`
    Deprecated bool   `json:"deprecated,omitempty"`
    Notes      string `json:"notes,omitempty"`
    
    // v2 新增：预检覆盖（可选）
    PreCheckOverride *CheckOverride `json:"preCheckOverride,omitempty"`
    
    // v2 新增：后检覆盖（可选）
    PostCheckOverride *CheckOverride `json:"postCheckOverride,omitempty"`
    
    // 保留 v1 字段用于向后兼容（已废弃）
    // Deprecated: 使用 PreCheckOverride 代替
    PreCheck []CheckStep `json:"preCheck,omitempty"`
    // Deprecated: 使用 PostCheckOverride 代替
    PostCheck []CheckStep `json:"postCheck,omitempty"`
}

// CheckOverride 检查覆盖配置
type CheckOverride struct {
    // 覆盖策略
    // - replace: 完全替换全局策略
    // - merge: 按 name 合并（路径覆盖全局同名项）
    // - append: 追加到全局策略后
    Override string `json:"override"`
    
    // 覆盖/追加的检查项
    Checks []CheckStep `json:"checks,omitempty"`
}
```

### 5.3 CheckResult 和 CheckReport

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
    OverrideSource string        `json:"overrideSource,omitempty"` // 覆盖来源
    ExecutedAt     metav1.Time   `json:"executedAt,omitempty"`
}

// ClusterVersionStatus 扩展
type ClusterVersionStatus struct {
    // ... 现有字段 ...
    
    PreCheckReport  *CheckReport `json:"preCheckReport,omitempty"`
    PostCheckReport *CheckReport `json:"postCheckReport,omitempty"`
}
```

### 5.4 CheckItem 接口

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

// CheckPolicyResolver 解析并合并检查策略
type CheckPolicyResolver struct {
    client client.Client
}

// ResolvePreChecks 解析预检策略
func (r *CheckPolicyResolver) ResolvePreChecks(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    edge *cvoapi.UpgradePathEdge,
) ([]cvoapi.CheckStep, string, error) {
    // 1. 获取全局 CheckPolicy
    globalPolicy, err := r.getGlobalCheckPolicy(ctx, cluster)
    if err != nil {
        return nil, "", fmt.Errorf("failed to get global CheckPolicy: %w", err)
    }
    
    globalChecks := globalPolicy.Spec.PreCheck
    
    // 2. 检查是否有路径覆盖
    if edge.PreCheckOverride == nil {
        return globalChecks, globalPolicy.Name, nil
    }
    
    // 3. 根据覆盖策略合并
    switch edge.PreCheckOverride.Override {
    case "replace":
        return edge.PreCheckOverride.Checks, edge.From + "->" + edge.To, nil
    case "merge":
        return mergeChecks(globalChecks, edge.PreCheckOverride.Checks), globalPolicy.Name + "+" + edge.From + "->" + edge.To, nil
    case "append":
        return append(globalChecks, edge.PreCheckOverride.Checks...), globalPolicy.Name + "+" + edge.From + "->" + edge.To, nil
    default:
        return globalChecks, globalPolicy.Name, nil
    }
}

// getGlobalCheckPolicy 获取全局 CheckPolicy
func (r *CheckPolicyResolver) getGlobalCheckPolicy(
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
        // 返回默认策略
        return r.getDefaultCheckPolicy(), nil
    }
    
    // 按优先级排序，返回最高优先级的
    sort.Slice(matchedPolicies, func(i, j int) bool {
        return matchedPolicies[i].Spec.Priority > matchedPolicies[j].Spec.Priority
    })
    
    return matchedPolicies[0], nil
}

// mergeChecks 按 name 合并检查项
func mergeChecks(global, override []cvoapi.CheckStep) []cvoapi.CheckStep {
    result := make([]cvoapi.CheckStep, len(global))
    copy(result, global)
    
    overrideMap := make(map[string]cvoapi.CheckStep)
    for _, step := range override {
        overrideMap[step.Name] = step
    }
    
    // 覆盖同名项
    for i, step := range result {
        if override, ok := overrideMap[step.Name]; ok {
            result[i] = override
            delete(overrideMap, step.Name)
        }
    }
    
    // 追加新项
    for _, step := range overrideMap {
        result = append(result, step)
    }
    
    return result
}

// getDefaultCheckPolicy 返回默认 CheckPolicy
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
    edge *cvoapi.UpgradePathEdge,
) (*CheckReport, error) {
    // 解析策略
    checks, source, err := r.resolver.ResolvePreChecks(ctx, cluster, edge)
    if err != nil {
        return nil, err
    }
    
    r.logger.Info("Running pre-checks", 
        "from", edge.From, 
        "to", edge.To,
        "policySource", source,
        "checkCount", len(checks),
    )
    
    report, err := r.runChecks(ctx, cluster, checks, "pre-check")
    if err != nil {
        return nil, err
    }
    
    report.PolicyName = source
    return report, nil
}

// RunPostChecks 执行后检
func (r *CheckRunner) RunPostChecks(
    ctx context.Context,
    cluster *bkev1beta1.BKECluster,
    edge *cvoapi.UpgradePathEdge,
) (*CheckReport, error) {
    // 解析策略（类似 RunPreChecks）
    checks, source, err := r.resolver.ResolvePostChecks(ctx, cluster, edge)
    if err != nil {
        return nil, err
    }
    
    r.logger.Info("Running post-checks",
        "from", edge.From,
        "to", edge.To,
        "policySource", source,
        "checkCount", len(checks),
    )
    
    report, err := r.runChecks(ctx, cluster, checks, "post-check")
    if err != nil {
        return nil, err
    }
    
    report.PolicyName = source
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

### 9.3 UpgradePath 路径覆盖

```yaml
# 升级路径（仅定义路径特定覆盖）
apiVersion: cvo.openfuyao.cn/v1beta1
kind: UpgradePath
metadata:
  name: openfuyao-upgrade-paths
spec:
  ociRef: "registry/openfuyao-upgradepath:latest"
  paths:
    - from: "v2.5.0"
      to: "v2.6.0"
      blocked: false
      # 路径特定覆盖：追加一个数据迁移检查
      preCheckOverride:
        override: "append"
        checks:
          - name: data-migration-check
            required: true
            timeout: "10m"
            params:
              migrationType: "schema-v2-to-v3"
      postCheckOverride:
        override: "append"
        checks:
          - name: data-integrity-check
            required: true
            timeout: "5m"
    
    - from: "v2.4.0"
      to: "v2.5.0"
      blocked: false
      # 无覆盖，使用全局 CheckPolicy
    
    - from: "v2.4.0"
      to: "v2.6.0"
      blocked: true
      notes: "Direct upgrade blocked, please upgrade via v2.5.0"
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
    overrideSource: "production+v2.5.0->v2.6.0"  # 策略来源
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
      - name: data-migration-check
        status: Passed
        message: "Data migration check passed"
        duration: "120s"
    totalDuration: "185.5s"
    passedCount: 5
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
        []string{"policy_name", "override_source"},
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
    
    // Step 4: 查找升级路径
    edge, err := r.upgradePathGraph.FindPath(cv.Status.CurrentVersion, cv.Spec.DesiredVersion)
    if err != nil {
        return r.updateStatus(ctx, cv, cvoapi.PhaseBlocked, "no valid upgrade path")
    }
    
    // Step 5: 执行预检（使用 CheckPolicyResolver 解析策略）
    preCheckReport, err := r.checkRunner.RunPreChecks(ctx, cluster, edge)
    if err != nil {
        return r.updateStatus(ctx, cv, cvoapi.PhaseFailed, fmt.Sprintf("pre-check error: %v", err))
    }
    
    cv.Status.PreCheckReport = preCheckReport
    r.recordCheckEvent(cv, "PreCheck", preCheckReport)
    
    if preCheckReport.Status == CheckStatusFailed {
        return r.updateStatus(ctx, cv, cvoapi.PhasePreCheckFailed, "pre-check failed")
    }
    
    // Step 6: 执行 DAG
    // ... 现有逻辑 ...
    
    // Step 7: 执行后检
    postCheckReport, err := r.checkRunner.RunPostChecks(ctx, cluster, edge)
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
| CheckPolicyResolver - 全局策略 | 无路径覆盖 | 返回全局 CheckPolicy |
| CheckPolicyResolver - replace | override=replace | 完全替换全局策略 |
| CheckPolicyResolver - merge | override=merge | 按 name 合并 |
| CheckPolicyResolver - append | override=append | 追加到全局策略 |
| CheckPolicyResolver - 优先级 | 多个 CheckPolicy 匹配 | 返回最高优先级 |
| CheckPolicyResolver - 选择器 | 集群标签匹配 | 返回匹配的 CheckPolicy |

### 13.2 集成测试

| 测试场景 | 测试内容 | 预期结果 |
|---------|---------|---------|
| 全局策略执行 | 仅配置全局 CheckPolicy | 使用全局策略执行检查 |
| 路径覆盖执行 | 配置路径覆盖 | 使用合并后策略执行检查 |
| 环境差异化 | 生产/测试环境不同策略 | 按环境选择正确策略 |

## 14. 工作量评估

| 阶段 | 任务内容 | 工作量 (人天) |
|------|---------|-------------|
| CheckPolicy CRD | CRD 定义、Webhook 验证 | 2 |
| CheckPolicyResolver | 策略解析、合并逻辑 | 3 |
| 检查框架 | CheckRegistry、CheckRunner | 3 |
| 预检项开发 | 4 个预检项实现 | 6 |
| 后检项开发 | 4 个后检项实现 | 5 |
| 集成开发 | ClusterVersionReconciler 集成 | 3 |
| 可观测性 | Prometheus 指标 | 2 |
| 测试 | 单元测试、集成测试 | 4 |
| **总计** | | **28 人天** |

## 15. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| CheckPolicy 配置错误 | 检查项缺失或错误 | Webhook 验证 CheckPolicy 合法性 |
| 策略合并逻辑复杂 | 难以预测最终策略 | 日志记录策略来源，支持 dry-run 预览 |
| 向后兼容问题 | 旧 UpgradePath 无法工作 | 保留 UpgradePathEdge.preCheck/postCheck 字段 |

## 16. 迁移计划

### 16.1 从 v1 迁移到 v2

1. **部署新 CRD**：部署 CheckPolicy CRD，保留 UpgradePath 的 preCheck/postCheck 字段
2. **创建默认 CheckPolicy**：基于现有 UpgradePath 中的检查项，创建全局 CheckPolicy
3. **灰度切换**：通过 Feature Gate 控制使用 v1 还是 v2 逻辑
4. **清理旧字段**：稳定后废弃 UpgradePathEdge.preCheck/postCheck 字段

---

**文档版本**: v2.0  
**维护者**: openFuyao Team

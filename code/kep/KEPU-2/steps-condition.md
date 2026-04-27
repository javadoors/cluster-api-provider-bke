# installAction.steps[].condition 设计方案

基于 `KEPU-2` 的现有设计，我提供一个完整的条件执行系统设计方案。

## 1. 核心设计原则

| 原则 | 说明 |
|------|------|
| **复用现有模板系统** | 基于已有的 Go `text/template` 系统，保持一致性 |
| **布尔结果明确** | 条件表达式最终必须返回 `true` 或 `false` 的字符串表示 |
| **丰富的内置函数** | 提供常用的比较、逻辑、字符串处理函数 |
| **步骤间数据传递** | 支持引用前序步骤的输出作为条件判断依据 |
| **错误处理友好** | 条件解析失败时提供清晰的错误信息，默认跳过步骤 |

## 2. 数据结构定义

### 2.1 扩展 ActionStep 结构

```go
type ActionStep struct {
    Name          string            `json:"name"`
    Type          ActionType        `json:"type"`
    Script        string            `json:"script,omitempty"`
    ScriptSource  *SourceSpec       `json:"scriptSource,omitempty"`
    Manifest      string            `json:"manifest,omitempty"`
    ManifestSource *SourceSpec      `json:"manifestSource,omitempty"`
    Chart         *ChartAction      `json:"chart,omitempty"`
    Kubectl       *KubectlAction    `json:"kubectl,omitempty"`
    
    // 条件表达式（新增详细设计）
    Condition     string            `json:"condition,omitempty"`
    
    // 条件失败策略
    OnConditionFail ConditionFailPolicy `json:"onConditionFail,omitempty"`
    
    OnFailure     FailurePolicy     `json:"onFailure,omitempty"`
    Retries       int               `json:"retries,omitempty"`
    NodeSelector  *NodeSelector     `json:"nodeSelector,omitempty"`
}

// 条件失败策略
type ConditionFailPolicy string

const (
    // 条件不满足时跳过步骤（默认）
    ConditionFailSkip ConditionFailPolicy = "Skip"
    // 条件不满足时标记为失败
    ConditionFailFail ConditionFailPolicy = "Fail"
    // 条件不满足时继续但记录警告
    ConditionFailWarn ConditionFailPolicy = "Warn"
)
```

## 3. 模板上下文扩展

### 3.1 扩展 TemplateContext 结构

```go
type TemplateContext struct {
    // ... 现有字段保持不变
    
    // 步骤输出引用
    Steps map[string]*StepOutput `json:"steps"`
    
    // 操作类型信息
    Operation OperationType `json:"operation"` // Install/Upgrade/Rollback/Uninstall
    
    // 前一个版本（仅升级/回滚时）
    PreviousVersion string `json:"previousVersion,omitempty"`
    
    // 节点序号信息（扩展）
    NodeIndex    int  `json:"nodeIndex"`    // 当前节点索引（0-based）
    NodeTotal    int  `json:"nodeTotal"`    // 总节点数
    IsFirstNode  bool `json:"isFirstNode"`  // 是否为首节点
    IsLastNode   bool `json:"isLastNode"`   // 是否为尾节点
    IsEtcdLeader bool `json:"isEtcdLeader"` // 是否为 etcd leader（仅 etcd 组件）
    
    // 组件状态信息
    ComponentStatus *ComponentStatusInfo `json:"componentStatus,omitempty"`
}

// 步骤输出
type StepOutput struct {
    Name      string            `json:"name"`
    Stdout    string            `json:"stdout"`
    Stderr    string            `json:"stderr"`
    ExitCode  int               `json:"exitCode"`
    Success   bool              `json:"success"`
    StartedAt metav1.Time       `json:"startedAt"`
    Duration  metav1.Duration   `json:"duration"`
    Outputs   map[string]string `json:"outputs,omitempty"` // 自定义输出键值对
}

// 组件状态信息
type ComponentStatusInfo struct {
    Phase          ComponentPhase `json:"phase"`
    InstalledVersion string       `json:"installedVersion,omitempty"`
    IsHealthy      bool           `json:"isHealthy"`
}
```

## 4. 内置模板函数库

### 4.1 比较函数

| 函数名 | 签名 | 说明 | 示例 |
|--------|------|------|------|
| `eq` | `eq a b` | 相等比较 | `{{eq .NodeIndex 0}}` |
| `ne` | `ne a b` | 不等比较 | `{{ne .Version "v1.0.0"}}` |
| `gt` | `gt a b` | 大于比较 | `{{gt .NodeTotal 1}}` |
| `ge` | `ge a b` | 大于等于 | `{{ge .NodeIndex 0}}` |
| `lt` | `lt a b` | 小于比较 | `{{lt .NodeIndex 3}}` |
| `le` | `le a b` | 小于等于 | `{{le .NodeIndex 2}}` |
| `semverCompare` | `semverCompare a b op` | 语义化版本比较 | `{{semverCompare .Version "v1.5.0" "ge"}}` |

### 4.2 逻辑函数

| 函数名 | 签名 | 说明 | 示例 |
|--------|------|------|------|
| `and` | `and a b c...` | 逻辑与 | `{{and .IsFirstNode .IsEtcdLeader}}` |
| `or` | `or a b c...` | 逻辑或 | `{{or (eq .NodeIndex 0) (eq .NodeIndex 1)}}` |
| `not` | `not a` | 逻辑非 | `{{not .IsHealthy}}` |

### 4.3 字符串函数

| 函数名 | 签名 | 说明 | 示例 |
|--------|------|------|------|
| `contains` | `contains substr str` | 包含判断 | `{{contains "etcd" .NodeRoles}}` |
| `hasPrefix` | `hasPrefix prefix str` | 前缀判断 | `{{hasPrefix "v1.5" .Version}}` |
| `hasSuffix` | `hasSuffix suffix str` | 后缀判断 | `{{hasSuffix ".0" .Version}}` |
| `regexMatch` | `regexMatch pattern str` | 正则匹配 | `{{regexMatch "^v[0-9]+\\.[0-9]+" .Version}}` |
| `len` | `len arr/str` | 长度计算 | `{{gt (len .NodeRoles) 1}}` |

### 4.4 集合函数

| 函数名 | 签名 | 说明 | 示例 |
|--------|------|------|------|
| `hasRole` | `hasRole role roles` | 角色存在判断 | `{{hasRole "etcd" .NodeRoles}}` |
| `inList` | `inList item list` | 列表包含判断 | `{{inList .NodeIP .EtcdEndpoints}}` |
| `indexOf` | `indexOf item list` | 获取索引 | `{{eq (indexOf .NodeIP .EtcdEndpoints) 0}}` |

### 4.5 步骤输出函数

| 函数名 | 签名 | 说明 | 示例 |
|--------|------|------|------|
| `stepSuccess` | `stepSuccess stepName` | 步骤是否成功 | `{{stepSuccess "check-version"}}` |
| `stepStdout` | `stepStdout stepName` | 获取步骤标准输出 | `{{contains "v3.5.12" (stepStdout "check-version")}}` |
| `stepExitCode` | `stepExitCode stepName` | 获取步骤退出码 | `{{eq (stepExitCode "check-version") 0}}` |
| `stepOutput` | `stepOutput stepName key` | 获取自定义输出 | `{{eq (stepOutput "detect-os" "OS_TYPE") "linux"}}` |

## 5. 条件表达式语法

### 5.1 基本语法

条件表达式使用 Go `text/template` 语法，最终必须返回字符串 `"true"` 或 `"false"`。

```yaml
# 简单示例
condition: "{{eq .NodeIndex 0}}"

# 复杂逻辑示例
condition: "{{and (hasRole \"etcd\" .NodeRoles) (eq .NodeIndex 0) (not .ComponentStatus.IsHealthy)}}"
```

### 5.2 布尔结果处理

| 表达式结果 | 处理方式 |
|-----------|---------|
| `"true"` | 执行步骤 |
| `"false"` | 跳过步骤 |
| 其他值 | 解析为失败，按 `OnConditionFail` 策略处理 |

## 6. 完整示例

### 6.1 etcd 组件条件执行示例

```yaml
apiVersion: cvo.openfuyao.cn/v1beta1
kind: ComponentVersion
metadata:
  name: etcd-v3.5.12
spec:
  componentName: etcd
  scope: Node
  versions:
    - version: v3.5.12
      installAction:
        steps:
          # 步骤1：检测操作系统类型
          - name: detect-os-type
            type: Script
            script: |
              OS_TYPE=$(uname -s | tr '[:upper:]' '[:lower:]')
              echo "OS_TYPE=$OS_TYPE"
              echo "::set-output OS_TYPE::$OS_TYPE"
            timeout: 10s
          
          # 步骤2：仅在 Linux 系统上安装依赖
          - name: install-linux-dependencies
            type: Script
            condition: "{{eq (stepOutput \"detect-os-type\" \"OS_TYPE\") \"linux\"}}"
            onConditionFail: Skip
            script: |
              apt-get update && apt-get install -y socat conntrack
            timeout: 300s
          
          # 步骤3：仅在第一个 etcd 节点备份数据
          - name: backup-etcd-on-first-node
            type: Script
            condition: "{{and (hasRole \"etcd\" .NodeRoles) (eq .NodeIndex 0)}}"
            script: |
              ETCDCTL_API=3 etcdctl snapshot save {{.EtcdConfig.DataDir}}/init-snapshot.db \
                --endpoints=https://127.0.0.1:2379 \
                --cacert={{.CertificatesDir}}/etcd/ca.crt
            timeout: 300s
          
          # 步骤4：安装 etcd（所有节点）
          - name: install-etcd
            type: Script
            script: |
              cp /tmp/etcd-{{.Version}}/etcd /usr/local/bin/
              cp /tmp/etcd-{{.Version}}/etcdctl /usr/local/bin/
              chmod +x /usr/local/bin/etcd
            timeout: 60s
          
          # 步骤5：仅在非首节点加入集群
          - name: join-etcd-cluster
            type: Script
            condition: "{{and (hasRole \"etcd\" .NodeRoles) (gt .NodeIndex 0)}}"
            script: |
              # 加入现有 etcd 集群
              echo "Joining etcd cluster as member {{.NodeIndex}}"
            timeout: 120s
          
          # 步骤6：仅在最后一个节点验证集群健康
          - name: verify-cluster-health
            type: Script
            condition: "{{and (hasRole \"etcd\" .NodeRoles) .IsLastNode}}"
            script: |
              ETCDCTL_API=3 etcdctl endpoint health --cluster \
                --endpoints={{range $i, $ep := .EtcdEndpoints}}{{if $i}},{{end}}https://{{$ep}}:2379{{end}}
            timeout: 60s
      
      upgradeFrom:
        - fromVersion: v3.5.11
          upgradeAction:
            steps:
              # 步骤1：检查是否需要升级
              - name: check-current-version
                type: Script
                script: |
                  CURRENT=$(etcdctl version | head -1 | awk '{print $2}')
                  echo "CURRENT_VERSION=$CURRENT"
                  echo "::set-output CURRENT_VERSION::$CURRENT"
                timeout: 30s
              
              # 步骤2：仅当当前版本确实是 v3.5.11 时才执行升级
              - name: perform-upgrade
                type: Script
                condition: "{{eq (stepOutput \"check-current-version\" \"CURRENT_VERSION\") \"v3.5.11\"}}"
                onConditionFail: Skip
                script: |
                  # 执行升级逻辑
                  systemctl stop etcd
                  cp /tmp/etcd-v3.5.12/etcd /usr/local/bin/
                  systemctl start etcd
                timeout: 300s
```

## 7. 实现流程

### 7.1 条件评估流程

```
┌─────────────────────────────────────────────────────────────────┐
│                   条件评估执行流程                                │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. 检查 ActionStep 是否有 Condition 字段                       │
│     └── 无 → 直接执行步骤                                       │
│                                                                 │
│  2. 构建完整的 TemplateContext（包含 Steps 历史输出）            │
│                                                                 │
│  3. 注册所有内置函数到模板引擎                                  │
│                                                                 │
│  4. 渲染条件表达式                                              │
│     ├── 成功 → 获取结果字符串                                   │
│     └── 失败 → 按 OnConditionFail 策略处理                       │
│                                                                 │
│  5. 解析渲染结果为布尔值                                        │
│     ├── "true" → 执行步骤                                       │
│     ├── "false" → 跳过步骤                                      │
│     └── 其他 → 解析失败，按策略处理                             │
│                                                                 │
│  6. 记录条件评估结果到状态（用于调试）                           │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### 7.2 条件引擎实现

```go
// ConditionEvaluator 条件评估器
type ConditionEvaluator struct {
    funcMap template.FuncMap
}

// NewConditionEvaluator 创建条件评估器
func NewConditionEvaluator() *ConditionEvaluator {
    return &ConditionEvaluator{
        funcMap: buildFuncMap(),
    }
}

// Evaluate 评估条件表达式
func (e *ConditionEvaluator) Evaluate(
    ctx context.Context,
    condition string,
    templateCtx *TemplateContext,
) (bool, error) {
    if condition == "" {
        return true, nil // 无条件表达式默认执行
    }

    // 创建模板
    tmpl, err := template.New("condition").
        Funcs(e.funcMap).
        Parse(condition)
    if err != nil {
        return false, fmt.Errorf("parse condition failed: %w", err)
    }

    // 渲染模板
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, templateCtx); err != nil {
        return false, fmt.Errorf("execute condition failed: %w", err)
    }

    result := strings.TrimSpace(buf.String())
    
    // 解析布尔结果
    switch strings.ToLower(result) {
    case "true", "yes", "1":
        return true, nil
    case "false", "no", "0":
        return false, nil
    default:
        return false, fmt.Errorf("condition result not boolean: %q", result)
    }
}

// 构建函数映射
func buildFuncMap() template.FuncMap {
    return template.FuncMap{
        // 比较函数
        "eq":           eq,
        "ne":           ne,
        "gt":           gt,
        "ge":           ge,
        "lt":           lt,
        "le":           le,
        "semverCompare": semverCompare,
        
        // 逻辑函数
        "and":          and,
        "or":           or,
        "not":          not,
        
        // 字符串函数
        "contains":     contains,
        "hasPrefix":    hasPrefix,
        "hasSuffix":    hasSuffix,
        "regexMatch":   regexMatch,
        "len":          length,
        
        // 集合函数
        "hasRole":      hasRole,
        "inList":       inList,
        "indexOf":      indexOf,
        
        // 步骤输出函数
        "stepSuccess":  stepSuccess,
        "stepStdout":   stepStdout,
        "stepExitCode": stepExitCode,
        "stepOutput":   stepOutput,
    }
}
```

## 8. 状态记录与调试

### 8.1 扩展状态记录

```go
// StepExecutionStatus 步骤执行状态
type StepExecutionStatus struct {
    Name           string               `json:"name"`
    Condition      string               `json:"condition,omitempty"`
    ConditionEval  string               `json:"conditionEval,omitempty"` // 条件评估结果
    ConditionPass  bool                 `json:"conditionPass,omitempty"`  // 条件是否通过
    Executed       bool                 `json:"executed"`
    StartedAt      *metav1.Time         `json:"startedAt,omitempty"`
    CompletedAt    *metav1.Time         `json:"completedAt,omitempty"`
    Duration       *metav1.Duration     `json:"duration,omitempty"`
    Success        bool                 `json:"success,omitempty"`
    ExitCode       int                  `json:"exitCode,omitempty"`
    Stdout         string               `json:"stdout,omitempty"`
    Stderr         string               `json:"stderr,omitempty"`
    CustomOutputs  map[string]string    `json:"customOutputs,omitempty"`
    Error          string               `json:"error,omitempty"`
}
```

### 8.2 自定义输出标记

支持在脚本中使用特殊格式标记自定义输出：

```bash
# 在脚本中设置自定义输出
echo "::set-output OS_TYPE::linux"
echo "::set-output NEEDS_UPGRADE::true"
```

解析器会自动提取这些键值对到 `StepOutput.CustomOutputs` 中。

## 9. 总结

本设计方案的核心特点：

| 特性 | 说明 |
|------|------|
| ✅ **复用现有模板系统** | 基于 Go `text/template`，与现有系统一致 |
| ✅ **丰富的内置函数** | 提供比较、逻辑、字符串、集合、步骤输出等函数 |
| ✅ **灵活的条件策略** | 支持 `Skip`/`Fail`/`Warn` 三种条件失败策略 |
| ✅ **步骤间数据传递** | 通过 `::set-output` 标记和 `stepOutput` 函数传递数据 |
| ✅ **节点序号感知** | 支持 `NodeIndex`、`IsFirstNode`、`IsLastNode` 等变量 |
| ✅ **角色判断** | 提供 `hasRole` 函数判断节点角色 |
| ✅ **语义化版本比较** | 支持 `semverCompare` 函数进行版本比较 |
| ✅ **完善的调试信息** | 记录条件表达式、评估结果、执行详情 |

此方案完全兼容 `KEPU-2` 的现有设计，可直接集成到 ActionEngine 中。
        

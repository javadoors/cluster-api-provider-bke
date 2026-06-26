# 分析代码库中缺少日志打印记录的代码，及日志级别记录不合理的地方，并给出整改方案

基于对代码库的全面分析，以下是日志打印记录的现状总结及系统性整改方案。

## 一、 现状分析总结

项目当前使用的是自定义日志门面 `utils/log/log.go`（底层封装 `ologger`），支持 `Trace/Debug/Info/Warn/Error/Critical` 级别，但**未使用** Kubernetes 生态标准的 `logr`/`klog`，也**完全没有使用** `log.V(x)` 进行日志级别控制。

主要问题集中在以下四个维度：

| 问题维度 | 严重程度 | 典型表现 |
|:---|:---:|:---|
| **缺失日志** | 🔴 高 | 控制器 `if err != nil` 静默返回；SSH/远程操作无入口/出口日志 |
| **级别误用** | 🟡 中 | 失败信息用 `Info` 打印；正常跳过逻辑用 `Error` 打印；循环内 `Info` 导致日志刷屏 |
| **上下文缺失** | 🔴 高 | 残留脚手架占位符 `"demoMachine"`；错误信息缺少资源名称/命名空间 |
| **静默吞错** | 🔴 高 | 错误被捕获后返回 `nil` 且不记录，导致 controller-runtime 无法触发重试 |

## 二、 核心问题分类与典型代码定位

### 2.1 缺失日志（Missing Logs）

**问题 A：错误静默返回**
控制器中大量 `if err != nil { return ctrl.Result{}, err }` 模式，在返回前没有任何日志记录，导致排查问题时无法定位是哪个环节出错。

- `controllers/capbke/bkemachine_controller.go:104-106` — patch helper 创建失败无日志
- `controllers/capbke/bkemachine_controller.go:123-126` — **严重**：`GetCombinedBKECluster` 失败后错误被吞掉（返回 `nil`），既不记录也不触发重试
- `controllers/capbke/bkecluster_controller.go:114-117` — `getOldBKECluster` 失败无日志
- `pkg/phaseframe/phases/ensure_master_upgrade.go:66-68` — 状态同步失败无日志

**问题 B：外部操作无追踪**
SSH 连接、远程命令执行、远程集群客户端创建等高风险操作缺乏入口/出口日志。

- `pkg/phaseframe/phaseutil/agentssh/push_upgrade.go` — SSH 升级操作无入口日志（哪些节点、什么操作）和出口汇总（成功/失败数量）
- `pkg/kube/kube.go:95-115, 158-180` — 远程客户端创建（kubeconfig/token fallback）无日志指示走了哪条路径

### 2.2 日志级别误用（Unreasonable Log Levels）

**问题 A：失败信息用 Info 打印**
- `utils/bkeagent/pkiutil/kubeconfig.go:253` — 证书生成失败用 `log.Infof`，应为 `Errorf`
- `pkg/phaseframe/phases/ensure_worker_delete.go:377` — 节点删除失败用 `Info`，应为 `Error`
- `controllers/capbke/bkemachine_controller.go:501` — reset 命令失败用 `Infof`，应为 `Warnf`

**问题 B：正常信息用 Error 打印**
- `controllers/capbke/bkecluster_controller.go:385` — 外部管理的集群跳过映射是正常逻辑，用 `Errorf` 会污染告警
- `pkg/phaseframe/phases/ensure_cluster.go:116` — **Bug**：此处 `err` 实际为 `nil`（已在上方处理），打印出空错误信息

**问题 C：循环/轮询内 Info 刷屏**
- `pkg/phaseframe/phases/phase_flow.go:216` — 每次 Phase 迭代都 `Infof("current phase name: %s")`，多 Phase 集群会产生大量噪音
- `controllers/capbke/bkemachine_controller_phases.go:211` — 每次 Reconcile 都打印 "Waiting for the control plane to be initialized"
- `pkg/kube/yaml.go:52-53` — 每个 YAML 文件操作都打印 `*****start*****` / `*****end*****`

### 2.3 日志上下文缺失（Poor Log Context）

**问题 A：脚手架残留**
- `controllers/capbke/bkemachine_controller_phases.go:192, 491, 834, 852` — 日志消息硬编码为 `"failed to patch demoMachine"`，`demoMachine` 是 kubebuilder 脚手架占位符，从未替换为实际资源名

**问题 B：模糊错误描述**
- `pkg/phaseframe/phases/ensure_cluster.go:110` — `"some err in ensureCluster.go: %s"`，用文件名代替操作描述，且聚合多个错误丢失细节
- `pkg/phaseframe/phaseutil/oauth.go:120` — `"cannot convert to unstructured object"`，无任何对象类型或名称信息

**问题 C：参数顺序错误**
- `pkg/phaseframe/phaseutil/bkecluster.go:134` — `log.Error(cmd.Name, err)`，`cmd.Name` 被当作消息，`err` 被当作 key-value 参数，语义完全错误

## 三、 整改方案

### 3.1 整改原则

1. **错误必记**：所有 `if err != nil` 分支在 `return` 前必须有 `Error` 或 `Warn` 级别日志
2. **级别准确**：失败/异常 → `Error`；预期内的降级/跳过 → `Warn`；正常业务流程 → `Info`；高频循环/调试 → `Debug`
3. **上下文完整**：每条日志必须包含 `资源名/命名空间` + `操作描述` + `错误对象`
4. **外部操作可追踪**：所有 SSH/远程 API 调用必须有入口日志（参数）和出口日志（结果/耗时）

### 3.2 具体整改措施

#### 措施 1：控制器错误日志补全（高优先级）

**目标文件**：`controllers/capbke/*.go`

**整改模式**：
```go
// 整改前
if err != nil {
    return ctrl.Result{}, err
}

// 整改后
if err != nil {
    log.Error("failed to [操作描述]", "bkemachine", bkemachine.Name, 
              "namespace", bkemachine.Namespace, "error", err)
    return ctrl.Result{}, err
}
```

**重点修复**：
- `bkemachine_controller.go:123-126` — 将 `return ctrl.Result{}, nil` 改为 `return ctrl.Result{}, err` 并添加日志，修复错误被吞没的 Bug
- 所有 `patch.NewHelper` 失败处补充日志

#### 措施 2：修复脚手架残留与上下文缺失（高优先级）

**目标文件**：`controllers/capbke/bkemachine_controller_phases.go`

**整改内容**：
- 将 `"failed to patch demoMachine"` 替换为 `fmt.Sprintf("failed to patch BKEMachine %s/%s", machine.Namespace, machine.Name)`
- 修复 `log.Error(cmd.Name, err)` 参数顺序，改为 `log.Error("command validation failed", "command", cmd.Name, "error", err)`

#### 措施 3：日志级别纠正（中优先级）

| 文件 | 行号 | 当前级别 | 应改为 | 原因 |
|:---|:---|:---|:---|:---|
| `pkiutil/kubeconfig.go` | 253 | `Infof` | `Errorf` | 证书生成失败是严重错误 |
| `ensure_worker_delete.go` | 377 | `Info` | `Error` | 节点删除失败需要告警 |
| `bkecluster_controller.go` | 385 | `Errorf` | `Infof` | 外部管理集群跳过是正常逻辑 |
| `ensure_cluster.go` | 116 | `Error` | 删除 | `err` 为 nil，此行为 Bug |

#### 措施 4：降噪处理（中优先级）

**目标文件**：`pkg/phaseframe/phases/phase_flow.go`, `pkg/kube/yaml.go`

**整改内容**：
- `phase_flow.go:216` — `Infof("current phase name: %s")` 改为 `Debugf`
- `bkemachine_controller_phases.go:211` — "Waiting for control plane" 改为 `Debugf` 或添加条件判断（仅首次打印）
- `yaml.go:52-53` — `*****start/end*****` 改为 `Debugf`，或仅在 `Error` 时打印详细信息

#### 措施 5：外部操作日志增强（中优先级）

**目标文件**：`pkg/phaseframe/phaseutil/agentssh/push_upgrade.go`, `pkg/kube/kube.go`

**整改模式**：
```go
// SSH 操作入口
log.Info("starting SSH upgrade", "nodes", nodeIPs, "targetVersion", version)
defer func(start time.Time) {
    log.Info("SSH upgrade completed", "succeeded", len(successNodes), 
             "failed", len(failedNodes), "duration", time.Since(start))
}(time.Now())

// 远程客户端创建
log.Debug("creating remote cluster client", "method", "kubeconfig")
// fallback 时
log.Debug("kubeconfig failed, falling back to token", "error", err)
```

## 四、 优先级与实施建议

| 阶段 | 范围 | 工作量 | 优先级 |
|:---|:---|:---|:---:|
| **Phase 1** | 修复静默吞错 Bug + 脚手架残留 `"demoMachine"` | 1-2 人天 | 🔴 P0 |
| **Phase 2** | 控制器全量 `if err != nil` 日志补全 | 2-3 人天 | 🔴 P0 |
| **Phase 3** | 日志级别纠正（Info↔Error） | 1 人天 | 🟡 P1 |
| **Phase 4** | 循环/轮询降噪（Info→Debug） | 1 人天 | 🟡 P1 |
| **Phase 5** | SSH/远程操作入口出口日志增强 | 1-2 人天 | 🟢 P2 |

**实施建议**：
1. **Phase 1 立即执行**：`bkemachine_controller.go:123-126` 的错误吞没是潜在的生产 Bug，应优先修复
2. **引入日志规范文档**：在项目中添加 `docs/logging-guidelines.md`，明确各级别使用标准，避免后续 PR 再次引入同类问题

# k8s社区里有对应的日志规范吗？（云原生或微服务）
我已经完成了对 K8s 社区日志规范的系统性调研。以下是完整的分析报告。

## K8s 社区日志规范体系总览

K8s 社区有一套**完整且成熟的日志规范体系**，由三个层次构成：
```
┌─────────────────────────────────────────────────────────┐
│  第 1 层：接口标准 — logr (go-logr/logr)                │
│  K8s / controller-runtime / 云原生生态统一日志接口       │
├─────────────────────────────────────────────────────────┤
│  第 2 层：实现标准 — klog v2 (kubernetes/klog)          │
│  K8s 官方日志实现，兼容 logr 接口                       │
├─────────────────────────────────────────────────────────┤
│  第 3 层：规范文档 — KEP-1602 + SIG-Instrumentation     │
│  结构化日志规范、日志级别定义、消息风格指南               │
└─────────────────────────────────────────────────────────┘
```

## 一、核心规范：KEP-1602 结构化日志

> 来源：[KEP-1602: Structured Logging](https://github.com/kubernetes/enhancements/tree/master/keps/sig-instrumentation/1602-structured-logging)

### 1.1 日志方法规范

K8s 社区**只推荐两个日志方法**，严格禁止使用 `Infof`/`Errorf` 等格式化方法：

| 方法 | 用途 | 签名 |
|:---|:---|:---|
| `klog.InfoS` | 常规信息日志 | `InfoS(msg string, keysAndValues ...interface{})` |
| `klog.ErrorS` | 错误日志 | `ErrorS(err error, msg string, keysAndValues ...interface{})` |

**示例**：
```go
// ✅ 推荐（结构化）
klog.InfoS("Pod status updated", "pod", "kube-system/kubedns", "status", "ready")
klog.ErrorS(err, "Failed to update pod status")

// ❌ 禁止（格式化字符串）
klog.Infof("Updated pod %s status to %s", podName, status)
```

### 1.2 日志级别规范（V-levels）

K8s 采用**数值化详细度级别**（V-levels），而非命名级别（Debug/Info/Warn/Error）：

| 级别 | 含义 | 典型内容 |
|:---|:---|:---|
| `V(0)` | **始终可见** | 编程错误、panic 信息、CLI 参数 |
| `V(1)` | **合理默认** | 配置信息、可自愈的重复错误 |
| `V(2)` | **推荐默认** ⭐ | HTTP 请求及状态码、系统状态变更、控制器事件、调度器日志 |
| `V(3)` | **扩展信息** | 更详细的系统状态变更 |
| `V(4)` | **调试级别** | 复杂代码段的调试日志 |
| `V(5)` | **追踪级别** | 错误前的上下文追踪、故障排查信息 |

> **关键原则**：`V(2)` 是大多数系统的推荐默认级别。开发和测试环境可运行 `V(3)` 或 `V(4)`。

### 1.3 消息风格指南

| 规则 | 示例 |
|:---|:---|
| 首字母大写 | ✅ `"Received HTTP request"` |
| 不以句号结尾 | ✅ `"Pod status updated"` ❌ `"Pod status updated."` |
| 使用主动语态 | ✅ `"Could not delete pod"` |
| 使用过去时 | ✅ `"Could not delete pod"` ❌ `"Cannot delete pod"` |
| 引用对象时说明类型 | ✅ `"Deleted pod"` ❌ `"Deleted"` |

### 1.4 对象引用规范

K8s 对象在日志中有**标准引用格式**：
```go
// 命名空间对象：namespace/name
klog.InfoS("Pod status updated", "pod", klog.KObj(pod))
// 输出: pod="kube-system/kubedns"

// 非命名空间对象：仅 name
klog.InfoS("Node unavailable", "node", klog.KRef("", "nodepool-1"))
```

### 1.5 错误日志核心原则

这是与我们代码库问题**最相关**的部分：

> **"不要在返回错误之前打印错误日志"** — 因为不确定调用者是否会优雅处理该错误。如果调用者处理了，打印错误日志反而会误导管理员。

正确做法：
```go
// ❌ 错误做法：返回错误前打印 Error 日志
if err != nil {
    klog.ErrorS(err, "Failed to get resource")  // 不要这样做
    return err
}

// ✅ 正确做法 1：用 fmt.Errorf 包装错误返回
if err != nil {
    return fmt.Errorf("get resource %s: %w", name, err)
}

// ✅ 正确做法 2：调试用途时用 V(4) + Info 级别
if err != nil {
    klog.V(4).InfoS("Failed to get resource (debug)", "err", err)
    return err
}

// ✅ 正确做法 3：在调用链顶端（无法再向上返回时）才用 ErrorS
// 仅在错误无法返回、必须在此处处理时才使用 ErrorS
```

## 二、接口标准：logr

> 来源：[go-logr/logr](https://github.com/go-logr/logr) — K8s / controller-runtime 统一日志接口

### 2.1 核心设计理念

`logr` 是 K8s 生态的**日志接口标准**，它不是日志实现，而是一个 API 契约：
```go
type Logger interface {
    Info(msg string, keysAndValues ...interface{})
    Error(err error, msg string, keysAndValues ...interface{})
    V(level int) Logger          // 详细度控制
    WithValues(keysAndValues ...interface{}) Logger  // 上下文注入
    WithName(name string) Logger // 命名空间
}
```

### 2.2 logr vs 我们当前的 ologger

| 特性 | logr 标准 | 我们的 ologger | 差距 |
|:---|:---|:---|:---|
| 结构化 key-value | ✅ `Info(msg, k1, v1, k2, v2)` | ❌ `Info(args...)` 无强制结构 | 🔴 大 |
| V-level 详细度 | ✅ `V(4).Info(...)` | ❌ 无 `V()` 方法 | 🔴 大 |
| 上下文传递 | ✅ `WithValues`/`WithName` | ❌ 无 | 🔴 大 |
| Error 带 err 参数 | ✅ `Error(err, msg, ...)` | ❌ `Error(msg, args...)` | 🟡 中 |
| JSON 输出 | ✅ 原生支持 | ❌ 仅文本 | 🟡 中 |
| controller-runtime 兼容 | ✅ 原生集成 | ❌ 需适配 | 🔴 大 |

## 三、controller-runtime 日志规范

> controller-runtime 是 K8s Operator 开发的标准框架，其日志规范直接影响我们的控制器代码。

### 3.1 控制器日志标准模式

```go
// controller-runtime 标准做法
func (r *MyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)  // 从 context 获取 logger
    
    log.Info("Reconciling", "resource", req.NamespacedName)
    
    obj := &MyResource{}
    if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
        // 不在这里打 Error 日志，直接返回
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 使用 V-level 控制详细度
    log.V(1).Info("Processing resource", "spec", obj.Spec)
    log.V(4).Info("Detailed debug info", "fullObject", obj)
    
    // 仅在调用链顶端、错误无法返回时才用 Error
    if err := r.doSomething(obj); err != nil {
        log.Error(err, "Failed to process resource", "resource", obj.Name)
        // 这里不返回 err，而是记录状态并重试
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }
    
    return ctrl.Result{}, nil
}
```

### 3.2 共享库不打印日志

> **"共享库（如 client-go）不应自行打印错误日志，而应只返回 error"** — K8s SIG-Instrumentation

这条规范直接对应我们代码库中 `pkg/kube/`、`pkg/command/` 等共享包的问题。

## 四、我们代码库与 K8s 规范的差距分析

| 差距类别 | K8s 规范要求 | 我们的现状 | 严重程度 |
|:---|:---|:---|:---:|
| **无结构化日志** | `InfoS(msg, k, v)` | `Info(args...)` 纯文本拼接 | 🔴 |
| **无 V-level** | `V(2).InfoS(...)` | 全部 `Info`，无详细度控制 | 🔴 |
| **错误前打日志** | 返回错误前不打 Error | 大量 `if err != nil { return err }` 无日志 | 🟡 |
| **错误吞没** | 不允许静默吞错 | 多处 `return ctrl.Result{}, nil` 吞掉 err | 🔴 |
| **Info 打错误** | 错误必须用 ErrorS | 多处 `Infof("failed to ...")` | 🟡 |
| **Error 打正常信息** | 正常逻辑不用 Error | `Errorf("externally managed, skipping")` | 🟡 |
| **无对象引用规范** | `klog.KObj(obj)` | 硬编码字符串，残留 `"demoMachine"` | 🔴 |
| **共享库打日志** | 共享库只返回 error | `pkg/kube/` 等包内自行打日志 | 🟡 |
| **无 context 传递** | `ctrl.LoggerFrom(ctx)` | 全局单例 logger | 🟡 |

## 五、整改方案规划

### 5.1 分阶段迁移路线

```
Phase 0 (P0 紧急)     Phase 1 (P1 高)        Phase 2 (P2 中)        Phase 3 (P3 低)
修复 Bug 级问题       对齐 K8s 规范          引入 logr 接口         全面结构化
━━━━━━━━━━━━━━━━━    ━━━━━━━━━━━━━━━━━     ━━━━━━━━━━━━━━━━━     ━━━━━━━━━━━━━━━━
• 修复错误吞没        • 日志级别纠正          • 封装 logr 适配器      • JSON 输出支持
• 修复 "demoMachine"  • 错误前不打 Error      • V-level 引入          • context 传递
• 共享库去除日志       • 对象引用规范化        • WithValues 上下文     • 日志 schema 校验
```

### 5.2 Phase 0：修复 Bug 级问题（1-2 人天）

| 问题 | 修复方式 |
|:---|:---|
| `bkemachine_controller.go:124` 错误吞没 | 改为 `return ctrl.Result{}, err` |
| `"failed to patch demoMachine"` 脚手架残留 | 替换为 `fmt.Sprintf("failed to patch BKEMachine %s/%s", ns, name)` |
| `ensure_cluster.go:116` nil error 打印 | 删除该日志行（err 为 nil） |

### 5.3 Phase 1：对齐 K8s 规范（3-5 人天）

**核心规则**：
```
规则 1: if err != nil { return err }  → 不加日志，用 fmt.Errorf 包装
规则 2: 调用链顶端无法返回时           → 才用 Error 级别
规则 3: 正常业务跳过/预期降级          → 用 Info 级别
规则 4: 循环/轮询内重复信息            → 用 Debug 级别
规则 5: 对象引用                       → 统一 "namespace/name" 格式
```

**对照表**：

| 当前代码 | 整改后 |
|:---|:---|
| `log.Infof("failed to generate cert: %v", err)` | `return fmt.Errorf("generate cert %s: %w", name, err)` |
| `log.Error("externally managed, skipping")` | `log.Info("Skipping externally managed cluster", "cluster", name)` |
| `log.Info("current phase name: %s", phase)` | `log.Debug("Processing phase", "phase", phase.Name())` |
| `log.Error("some err in ensureCluster.go")` | `log.Error("Failed to ensure cluster health", "error", err, "cluster", name)` |

### 5.4 Phase 2：引入 logr 接口（5-7 人天）

在 `utils/log/` 中封装 logr 适配器，使现有代码渐进式迁移：
```go
// utils/log/logr_adapter.go
type Logger struct {
    inner ologger.Logger
}

func (l Logger) Info(msg string, keysAndValues ...interface{}) {
    l.inner.Info(buildMessage(msg, keysAndValues...))
}

func (l Logger) Error(err error, msg string, keysAndValues ...interface{}) {
    l.inner.Error(buildMessage(msg, keysAndValues...), err)
}

func (l Logger) V(level int) Logger {
    if level <= currentVerbosity {
        return l
    }
    return discardLogger // 静默
}

func (l Logger) WithValues(keysAndValues ...interface{}) Logger {
    // 将 key-value 对注入后续所有日志
    return Logger{inner: l.inner.WithPrefix(buildPrefix(keysAndValues...))}
}
```

### 5.5 Phase 3：全面结构化（7-10 人天）

- 支持 `--logging-format=json` 输出
- 通过 `context.Context` 传递 logger
- 引入日志 schema 静态校验（防止新增非结构化日志）

## 六、建议与下一步

### 当前最紧迫的 3 件事

1. **修复错误吞没 Bug** — `bkemachine_controller.go:124` 的 `return nil` 会导致控制器不重试
2. **清理脚手架残留** — 全局搜索 `"demoMachine"` 并替换
3. **建立日志规范文档** — 在项目中添加 `docs/logging-guidelines.md`，将上述 K8s 规范本地化

### 需要您决策的问题

1. **是否引入 logr 接口？** — 这是 K8s 生态标准，但需要封装适配层。短期可先对齐规范，长期再引入接口。
2. **是否支持 JSON 日志输出？** — 生产环境日志采集（ELK/Loki）通常需要 JSON 格式，但改造成本较高。
3. **整改优先级？** — 建议先做 Phase 0 + Phase 1（约 5-7 人天），解决 80% 的问题。

# logr的标准使用方法

`logr` 是 Kubernetes 生态（controller-runtime、client-go、klog v2）的统一日志接口标准。它不是日志实现，而是一个 **API 契约**，将"日志调用"与"日志输出"彻底解耦。

## 一、核心设计理念

```
┌──────────────────────────────────────────────────────┐
│  应用代码 / 库代码                                    │
│  只依赖 logr.Logger 接口（零实现依赖）                 │
├──────────────────────────────────────────────────────┤
│  logr.Logger 接口                                    │
│  Info / Error / V / WithValues / WithName            │
├──────────────────────────────────────────────────────┤
│  LogSink 实现层（可替换）                              │
│  zapr(zap) / klogr(klog) / zerologr / stdr / testr  │
└──────────────────────────────────────────────────────┘
```
**关键原则**：
- 应用代码只依赖 `logr.Logger`，不依赖任何具体日志库
- 日志实现由 `main()` 入口处决定，运行时可替换
- 结构化日志：消息 + key-value 对，禁止格式化字符串

## 二、基本 API

### 2.1 两个核心方法

```go
// Info：常规信息日志
func (l Logger) Info(msg string, keysAndValues ...interface{})
// Error：错误日志（err 是第一个参数）
func (l Logger) Error(err error, msg string, keysAndValues ...interface{})
```

**使用示例**：
```go
log.Info("Pod status updated", "pod", "kube-system/kubedns", "status", "ready")
// 输出: "Pod status updated" pod="kube-system/kubedns" status="ready"

log.Error(err, "Failed to update pod status", "pod", pod.Name)
// 输出: "Failed to update pod status" err="timeout" pod="kubedns"
```

### 2.2 V-level 详细度控制

```go
// V(level) 返回一个新的 Logger，只有当 level <= 当前设置的详细度时才会输出
log.V(0).Info("Always visible")           // 始终可见
log.V(1).Info("Reasonable default")       // 合理默认
log.V(2).Info("Recommended default")      // 推荐默认 ⭐
log.V(4).Info("Debug level")              // 调试级别
log.V(5).Info("Trace level")              // 追踪级别
```

**级别语义**：

| 级别 | 含义 | 典型场景 |
|:---:|:---|:---|
| `V(0)` | 始终可见 | 编程错误、panic、CLI 参数 |
| `V(1)` | 合理默认 | 配置信息、可自愈的重复错误 |
| `V(2)` | **推荐默认** | HTTP 请求、状态变更、控制器事件 |
| `V(3)` | 扩展信息 | 更详细的状态变更 |
| `V(4)` | 调试 | 复杂代码段调试 |
| `V(5)` | 追踪 | 错误前上下文、故障排查 |

### 2.3 上下文注入

```go
// WithValues：注入 key-value 对，后续所有日志自动携带
log := log.WithValues("controller", "bkecluster", "namespace", "default")
log.Info("Reconciling")
// 输出: "Reconciling" controller="bkecluster" namespace="default"

// WithName：给 logger 命名（层级化）
log := log.WithName("reconciler").WithName("phase")
log.Info("Starting phase")
// 输出: "Starting phase" logger="reconciler/phase"
```

## 三、controller-runtime 中的标准用法

### 3.1 从 Context 获取 Logger

```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // ✅ 标准做法：从 context 获取 logger（自动携带 request 信息）
    log := ctrl.LoggerFrom(ctx)

    log.Info("Reconciling BKECluster", "resource", req.NamespacedName)

    cluster := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
        // ✅ 不在这里打 Error 日志，直接返回
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // ✅ 使用 V-level 控制详细度
    log.V(1).Info("Processing cluster", "spec", cluster.Spec)
    log.V(4).Info("Detailed debug", "fullObject", cluster)

    return ctrl.Result{}, nil
}
```

### 3.2 向 Context 注入 Logger

```go
// 在 main() 或 SetupWithManager 中
func (r *BKEClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&bkev1beta1.BKECluster{}).
        Complete(r)
}
// controller-runtime 自动将 logger 注入到 Reconcile 的 ctx 中
```

### 3.3 手动注入额外上下文

```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)

    cluster := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // ✅ 将 cluster 信息注入 logger，传递给子函数
    ctx = ctrl.LoggerIntoContext(ctx, log.WithValues(
        "cluster", cluster.Name,
        "version", cluster.Spec.OpenFuyaoVersion,
    ))

    return r.reconcilePhases(ctx, cluster)
}

func (r *BKEClusterReconciler) reconcilePhases(ctx context.Context, cluster *bkev1beta1.BKECluster) (ctrl.Result, error) {
    // 子函数自动继承上下文中的 logger
    log := ctrl.LoggerFrom(ctx)
    log.Info("Starting phase reconciliation")
    // 输出自动包含 cluster 和 version 字段
    return ctrl.Result{}, nil
}
```

## 四、错误处理规范（最重要）

### 4.1 核心原则

> **"不要在返回错误之前打印 Error 日志"**

因为不确定调用者是否会优雅处理该错误。如果调用者处理了，打印 Error 日志会误导运维人员。

### 4.2 正确模式

```go
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 模式 1：可返回错误时 → 不打印日志，用 fmt.Errorf 包装
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
func (r *Reconciler) getResource(ctx context.Context, name string) (*Resource, error) {
    obj := &Resource{}
    if err := r.Get(ctx, types.NamespacedName{Name: name}, obj); err != nil {
        // ✅ 不打印日志，包装错误返回
        return nil, fmt.Errorf("get resource %s: %w", name, err)
    }
    return obj, nil
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 模式 2：调试用途 → V(4) + Info
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
func (r *Reconciler) getResource(ctx context.Context, name string) (*Resource, error) {
    log := ctrl.LoggerFrom(ctx)
    obj := &Resource{}
    if err := r.Get(ctx, types.NamespacedName{Name: name}, obj); err != nil {
        // ✅ 仅调试时可见
        log.V(4).Info("Failed to get resource", "name", name, "err", err)
        return nil, err
    }
    return obj, nil
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 模式 3：调用链顶端（无法再向上返回）→ 才用 Error
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)

    if err := r.doSomething(ctx); err != nil {
        // ✅ 这里是调用链顶端，错误无法再向上返回
        log.Error(err, "Failed to reconcile", "resource", req.NamespacedName)
        // 记录状态，不返回 err（避免无限重试）
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }
    return ctrl.Result{}, nil
}
```

## 五、共享库日志规范

> **"共享库不应自行打印日志，而应只返回 error"** — K8s SIG-Instrumentation
```go
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// ❌ 错误做法：共享库自行打印日志
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// pkg/kube/kube.go
func NewRemoteClient(cluster *BKECluster) (*Client, error) {
    client, err := buildClient(cluster)
    if err != nil {
        log.Errorf("failed to create client for %s: %v", cluster.Name, err)  // ❌
        return nil, err
    }
    return client, nil
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// ✅ 正确做法：共享库只返回 error
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// pkg/kube/kube.go
func NewRemoteClient(cluster *BKECluster) (*Client, error) {
    client, err := buildClient(cluster)
    if err != nil {
        return nil, fmt.Errorf("create remote client for cluster %s/%s: %w",
            cluster.Namespace, cluster.Name, err)
    }
    return client, nil
}

// 调用方决定是否打印日志
// controllers/bkecluster_controller.go
client, err := kube.NewRemoteClient(cluster)
if err != nil {
    log.Error(err, "Failed to create remote client")  // ✅ 调用方打印
    return ctrl.Result{}, err
}
```

## 六、对象引用规范

```go
// K8s 标准：namespace/name 格式
log.Info("Processing pod", "pod", klog.KObj(pod))
// 输出: "Processing pod" pod="kube-system/kubedns"

// 非命名空间对象
log.Info("Node unavailable", "node", klog.KRef("", "node-1"))
// 输出: "Node unavailable" node="node-1"

// 在 logr 中没有 klog.KObj，手动构造
log.Info("Processing cluster", "cluster",
    fmt.Sprintf("%s/%s", cluster.Namespace, cluster.Name))
```

## 七、完整示例：控制器标准日志模式

```go
package controllers

import (
    "context"
    "fmt"

    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/log"
)

type BKEClusterReconciler struct {
    client.Client
}

func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 从 context 获取 logger
    log := ctrl.LoggerFrom(ctx)

    // 2. 入口日志（V(0) 始终可见）
    log.Info("Reconciling BKECluster")

    // 3. 获取资源（不打印日志，直接返回）
    cluster := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 4. 注入上下文
    log = log.WithValues("cluster", cluster.Name, "version", cluster.Spec.OpenFuyaoVersion)
    ctx = ctrl.LoggerIntoContext(ctx, log)

    // 5. 业务逻辑（V(1) 合理默认）
    log.V(1).Info("Processing cluster spec")

    // 6. 调用子函数
    if err := r.reconcilePhases(ctx, cluster); err != nil {
        // 7. 调用链顶端才用 Error
        log.Error(err, "Failed to reconcile phases")
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }

    // 8. 完成日志
    log.Info("Reconciliation complete")
    return ctrl.Result{}, nil
}

func (r *BKEClusterReconciler) reconcilePhases(ctx context.Context, cluster *bkev1beta1.BKECluster) error {
    log := ctrl.LoggerFrom(ctx)

    for _, phase := range phases {
        // V(2) 推荐默认：每个 phase 的执行状态
        log.V(2).Info("Executing phase", "phase", phase.Name())

        if err := phase.Execute(ctx, cluster); err != nil {
            // 包装错误返回，不打印
            return fmt.Errorf("execute phase %s: %w", phase.Name(), err)
        }

        log.V(2).Info("Phase completed", "phase", phase.Name())
    }
    return nil
}
```

## 八、速查表

| 场景 | 正确做法 | 错误做法 |
|:---|:---|:---|
| 可返回错误 | `return fmt.Errorf("...: %w", err)` | `log.Error(err, "..."); return err` |
| 调用链顶端 | `log.Error(err, "...")` | 静默吞错 |
| 调试信息 | `log.V(4).Info("...")` | `log.Info("...")` |
| 循环内日志 | `log.V(2).Info("...")` | `log.Info("...")`（刷屏） |
| 正常跳过 | `log.Info("Skipping...", "reason", "...")` | `log.Error("Skipping...")` |
| 共享库 | 只返回 `error` | 自行打印日志 |
| 对象引用 | `"namespace/name"` 格式 | 硬编码字符串 |
| 上下文传递 | `WithValues` + `LoggerIntoContext` | 全局单例 logger |

# V-level 详细度控制

## 一、核心概念

V-level 是 logr 的**数值化日志详细度机制**，用一个整数控制日志是否输出：
```
V(0) ──── 始终输出（最重要）
V(1) ──── 合理默认
V(2) ──── 推荐默认 ⭐
V(3) ──── 扩展信息
V(4) ──── 调试级别
V(5) ──── 追踪级别（最详细）

数字越大 → 越不重要 → 只在需要时才显示
```

**运行时规则**：程序启动时设置一个最大级别 `--v=N`，只有 `V(x)` 中 `x <= N` 的日志才会输出。
```
--v=0  →  只输出 V(0)                    → 生产环境最小噪音
--v=2  →  输出 V(0), V(1), V(2)          → 生产环境推荐默认
--v=4  →  输出 V(0) ~ V(4)               → 开发/调试环境
--v=5  →  全部输出                        → 深度故障排查
```

## 二、为什么需要 V-level

### 没有 V-level 的痛点

```go
// 所有日志都是 Info 级别，无法区分重要性
log.Info("Reconciling BKECluster")           // 每次调谐都打印
log.Info("Processing phase EnsureMasterInit") // 每个 phase 都打印
log.Info("Checking node 192.168.1.1")        // 每个节点都打印
log.Info("Checking node 192.168.1.2")        // 每个节点都打印
log.Info("Checking node 192.168.1.3")        // 每个节点都打印
// ... 100 个节点 = 100 行日志
```
**结果**：生产环境日志爆炸，真正重要的信息被淹没。

### 有 V-level 的效果

```go
log.Info("Reconciling BKECluster")                    // V(0) 始终可见
log.V(2).Info("Processing phase", "phase", "MasterInit") // V(2) 默认可见
log.V(4).Info("Checking node", "node", "192.168.1.1")   // V(4) 仅调试可见
log.V(4).Info("Checking node", "node", "192.168.1.2")   // V(4) 仅调试可见
log.V(4).Info("Checking node", "node", "192.168.1.3")   // V(4) 仅调试可见
```

| 运行级别 | 输出行数 | 看到的内容 |
|:---:|:---:|:---|
| `--v=0` | **1 行** | `Reconciling BKECluster` |
| `--v=2` | **2 行** | + `Processing phase` |
| `--v=4` | **5 行** | + 每个节点的检查日志 |

## 三、实际样例

### 3.1 控制器调谐场景

```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := ctrl.LoggerFrom(ctx)

    // V(0): 始终可见 — 调谐入口
    log.Info("Reconciling BKECluster")

    cluster := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // V(1): 合理默认 — 关键状态信息
    log.V(1).Info("Cluster state",
        "currentVersion", cluster.Status.CurrentVersion,
        "desiredVersion", cluster.Spec.DesiredVersion,
        "phase", cluster.Status.Phase)

    // V(2): 推荐默认 — 每个 phase 的执行状态
    for _, phase := range r.phases {
        log.V(2).Info("Executing phase", "phase", phase.Name())

        if err := phase.Execute(ctx, cluster); err != nil {
            log.Error(err, "Phase failed", "phase", phase.Name())
            return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
        }

        log.V(2).Info("Phase completed", "phase", phase.Name())
    }

    // V(4): 调试级别 — 详细内部状态
    log.V(4).Info("Reconcile finished",
        "status", cluster.Status,
        "annotations", cluster.Annotations,
        "finalizers", cluster.Finalizers)

    return ctrl.Result{}, nil
}
```

**不同级别的效果**：
```bash
# --v=0（生产最小噪音）
I "Reconciling BKECluster"

# --v=2（生产推荐）
I "Reconciling BKECluster"
I "Cluster state" currentVersion="v2.5.0" desiredVersion="v2.6.0" phase="Upgrading"
I "Executing phase" phase="EnsureProviderSelfUpgrade"
I "Phase completed" phase="EnsureProviderSelfUpgrade"
I "Executing phase" phase="EnsureAgentUpgrade"
I "Phase completed" phase="EnsureAgentUpgrade"
...

# --v=4（调试模式）
# 以上全部 + 每个节点的详细检查、每个 YAML 的解析过程、SSH 连接详情等
```

### 3.2 SSH 远程操作场景

```go
func (s *SSHExecutor) ExecuteOnNodes(ctx context.Context, nodes []Node, cmd string) error {
    log := ctrl.LoggerFrom(ctx)

    // V(0): 始终可见 — 操作概要
    log.Info("Executing SSH command on nodes",
        "nodeCount", len(nodes),
        "command", cmd)

    var failedNodes []string
    for _, node := range nodes {
        // V(4): 调试级别 — 每个节点的详细操作
        log.V(4).Info("Connecting to node",
            "node", node.IP,
            "port", node.Port,
            "user", node.User)

        result, err := s.sshClient.Run(node, cmd)
        if err != nil {
            // V(1): 合理默认 — 失败节点需要关注
            log.V(1).Info("SSH command failed on node",
                "node", node.IP,
                "error", err)
            failedNodes = append(failedNodes, node.IP)
            continue
        }

        // V(5): 追踪级别 — 命令输出详情
        log.V(5).Info("SSH command output",
            "node", node.IP,
            "stdout", result.Stdout,
            "stderr", result.Stderr,
            "exitCode", result.ExitCode)
    }

    // V(0): 始终可见 — 操作结果汇总
    log.Info("SSH command completed",
        "succeeded", len(nodes)-len(failedNodes),
        "failed", len(failedNodes),
        "failedNodes", failedNodes)

    if len(failedNodes) > 0 {
        return fmt.Errorf("SSH failed on %d nodes", len(failedNodes))
    }
    return nil
}
```

**效果对比**：
```bash
# --v=0（生产环境，只看概要和结果）
I "Executing SSH command on nodes" nodeCount=50 command="systemctl restart kubelet"
I "SSH command completed" succeeded=50 failed=0 failedNodes=[]

# --v=1（生产环境 + 失败信息）
I "Executing SSH command on nodes" nodeCount=50 command="systemctl restart kubelet"
I "SSH command failed on node" node="192.168.1.15" error="connection refused"
I "SSH command completed" succeeded=49 failed=1 failedNodes=["192.168.1.15"]

# --v=4（调试，看每个节点的连接过程）
I "Executing SSH command on nodes" nodeCount=50 command="systemctl restart kubelet"
I "Connecting to node" node="192.168.1.1" port=22 user="root"
I "Connecting to node" node="192.168.1.2" port=22 user="root"
... (50 行)
I "SSH command completed" succeeded=50 failed=0 failedNodes=[]

# --v=5（深度排查，看每个节点的命令输出）
# 以上全部 + 每个节点的 stdout/stderr/exitCode
```

### 3.3 DAG 升级调度场景

```go
func (r *UpgradeScheduler) ExecuteDAG(ctx context.Context, dag *DAG) error {
    log := ctrl.LoggerFrom(ctx)

    // V(0): 升级开始
    log.Info("Starting upgrade DAG execution",
        "totalComponents", dag.NodeCount())

    batches := dag.TopologicalSort()

    // V(2): 每个批次
    for i, batch := range batches {
        log.V(2).Info("Processing DAG batch",
            "batch", i+1,
            "totalBatches", len(batches),
            "components", batch)

        for _, comp := range batch {
            // V(3): 每个组件
            log.V(3).Info("Upgrading component",
                "component", comp.Name,
                "fromVersion", comp.CurrentVersion,
                "toVersion", comp.TargetVersion)

            // V(4): 组件内部细节
            log.V(4).Info("Resolving component manifest",
                "component", comp.Name,
                "ociRef", comp.OCIRef)

            if err := r.upgradeComponent(ctx, comp); err != nil {
                log.Error(err, "Component upgrade failed",
                    "component", comp.Name)
                return err
            }

            log.V(3).Info("Component upgraded successfully",
                "component", comp.Name)
        }
    }

    // V(0): 升级完成
    log.Info("Upgrade DAG execution completed",
        "totalComponents", dag.NodeCount(),
        "totalBatches", len(batches))

    return nil
}
```

**效果**：
```bash
# --v=0（运维只看开始和结束）
I "Starting upgrade DAG execution" totalComponents=12
I "Upgrade DAG execution completed" totalComponents=12 totalBatches=4

# --v=2（运维看批次进度）
I "Starting upgrade DAG execution" totalComponents=12
I "Processing DAG batch" batch=1 totalBatches=4 components=["provider-upgrade"]
I "Processing DAG batch" batch=2 totalBatches=4 components=["agent-upgrade","etcd"]
I "Processing DAG batch" batch=3 totalBatches=4 components=["kubernetes"]
I "Processing DAG batch" batch=4 totalBatches=4 components=["addon-calico","addon-coredns"]
I "Upgrade DAG execution completed" totalComponents=12 totalBatches=4

# --v=3（看每个组件的升级过程）
# 以上 + 每个组件的 from/to 版本

# --v=4（看 OCI 解析等内部细节）
# 以上 + manifest 解析、SSH 连接等
```

## 四、V-level 选择决策树

```
这条日志在生产环境中是否始终需要看到？
├── 是 → V(0)
│       例：调谐开始/结束、升级开始/结束、严重状态变更
│
└── 否 → 这条日志在正常运维中是否有用？
    ├── 是 → V(1) 或 V(2)
    │       V(1): 配置信息、可自愈的警告
    │       V(2): 状态变更、控制器事件、HTTP 请求 ⭐ 推荐默认
    │
    └── 否 → 这条日志是否帮助定位问题？
        ├── 是 → V(3) 或 V(4)
        │       V(3): 扩展状态信息
        │       V(4): 调试级别（复杂代码段）
        │
        └── 否 → V(5)
                追踪级别（错误前上下文、极细粒度）
```

## 五、关键总结

| 要点 | 说明 |
|:---|:---|
| **核心作用** | 让同一份代码在不同环境下输出不同详细度的日志 |
| **生产环境** | `--v=2`，只看到关键状态变更，日志量可控 |
| **调试环境** | `--v=4`，看到所有细节，无需改代码重新编译 |
| **故障排查** | `--v=5`，看到最细粒度的追踪信息 |
| **核心价值** | **不改代码、不重新编译，仅通过启动参数控制日志详细度** |

4. **考虑渐进式迁移至 `logr`**：当前自定义 `ologger` 缺乏 `V(x)` 支持，长期建议封装 `logr` 接口以兼容 controller-runtime 生态，但短期内可在 `ologger` 层面通过 `Debug` 级别实现类似效果

# V-level 的数值有规范吗？
V-level 有明确的社区规范。K8s SIG-Instrumentation 和 logr 项目各自给出了定义，两者高度一致。

## 一、K8s 官方规范（SIG-Instrumentation）

来源：[kubernetes/community/contributors/devel/sig-instrumentation/logging.md](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md)

这是 K8s 项目自身的**强推荐规范**，所有 K8s 组件（kube-apiserver、kubelet、controller-manager 等）都遵循此标准：

| 级别 | 定义 | 典型内容 |
|:---:|:---|:---|
| **V(0)** | **始终可见** — 集群运维人员必须看到 | 编程错误、panic 附加信息、CLI 参数处理 |
| **V(1)** | **合理默认** — 不想太详细时可关闭 | 配置信息（监听端口 X、监听资源 Y）、可自愈的重复错误（Pod 检测为不健康） |
| **V(2)** | **推荐默认** ⭐ — 大多数系统的推荐级别 | HTTP 请求及状态码、系统状态变更（杀 Pod）、控制器状态变更事件（启动 Pod）、调度器日志 |
| **V(3)** | **扩展信息** — 关于变更的更多细节 | 更详细的系统状态变更信息 |
| **V(4)** | **调试级别** — 开发/排查时使用 | 复杂代码段的调试日志，后续可能需要回来检查的地方 |
| **V(5)** | **追踪级别** — 深度故障排查 | 理解错误/告警前因的上下文、排查已报告问题的更多信息 |

> **K8s 的实践默认级别是 V(2)**。开发和测试环境可运行 V(3) 或 V(4)。

## 二、logr 官方指南

来源：[go-logr/logr README - How do I choose my V-levels?](https://github.com/go-logr/logr#how-do-i-choose-my-v-levels)

logr 给出了更灵活的指导原则：

> **唯一的硬约束：数字越大，日志越详细/越偏向调试。**

logr 的建议起点：

| 级别 | logr 建议 |
|:---:|:---|
| **0** | "你始终想看到这个" |
| **1** | "常见日志，你**可能**想关掉" |
| **10** | "我想压测你的日志采集系统" |

> 然后根据需要**从 10 往下选**（调试/追踪类）和**从 1 往上选**（较详细的 info 类）。

logr 还提到 Go 标准库 `slog` 预定义了 `-4` 对应 debug 日志（等价于 logr 的 `V(4)`），这与 K8s 推荐的调试级别一致。

## 三、两套规范的对应关系

| logr 级别 | K8s 级别 | slog 级别 | 语义 |
|:---:|:---:|:---:|:---|
| V(0) | V(0) | LevelInfo (0) | 始终可见 |
| V(1) | V(1) | — | 合理默认 |
| **V(2)** | **V(2)** ⭐ | — | **推荐默认** |
| V(3) | V(3) | — | 扩展信息 |
| **V(4)** | **V(4)** | **LevelDebug (-4)** | **调试级别** |
| V(5) | V(5) | — | 追踪级别 |
| V(6+) | — | — | 更细粒度（logr 独有） |

## 四、核心规则总结

```
硬约束（必须遵守）：
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  数字越大 → 越不重要 → 越偏向调试
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

软约束（社区惯例）：
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  V(0)  = 始终可见（生产必须）
  V(2)  = 推荐默认（生产推荐）⭐
  V(4)  = 调试级别（开发/排查）
  V(5+) = 追踪级别（深度排查）
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

选择方法：
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  从 0 往上 → 越来越不重要的 info 日志
  从 10 往下 → 越来越重要的 debug 日志
  两端向中间靠拢，直到覆盖所有场景
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```
**一句话**：K8s 社区有明确的 V(0)~V(5) 六级规范，V(2) 是生产推荐默认级别，V(4) 是调试级别。logr 本身只规定"数字越大越详细"的方向性约束，具体数值由项目自行定义，但 K8s 生态普遍遵循上述六级标准。

# 

# openFuyao 社区日志规范

> **版本**: v1.0  
> **状态**: 正式生效  
> **适用范围**: cluster-api-provider-bke 全代码库  
> **参考标准**: [K8s SIG-Instrumentation Logging](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-instrumentation/logging.md) · [logr](https://github.com/go-logr/logr)

---

## 目录

- [1. 概述](#1-概述)
- [2. 日志框架](#2-日志框架)
- [3. V-level 日志级别规范](#3-v-level-日志级别规范)
- [4. 结构化日志规范](#4-结构化日志规范)
- [5. 错误处理规范](#5-错误处理规范)
- [6. 控制器日志规范](#6-控制器日志规范)
- [7. 共享库日志规范](#7-共享库日志规范)
- [8. 对象引用规范](#8-对象引用规范)
- [9. Phase 日志规范](#9-phase-日志规范)
- [10. 禁止事项清单](#10-禁止事项清单)
- [11. 迁移指南](#11-迁移指南)
- [12. 速查表](#12-速查表)

---

## 1. 概述

### 1.1 背景

当前代码库日志存在以下核心问题：错误静默返回、日志级别误用、上下文缺失、脚手架残留。本规范旨在统一日志标准，对齐 Kubernetes 社区最佳实践，提升生产环境的可观测性与故障排查效率。

### 1.2 三条核心原则

```
原则 1: 结构化 — 日志必须是 key-value 结构化数据，禁止纯文本拼接
原则 2: 级别准确 — 失败用 Error，正常用 Info，调试用 Debug，禁止混用
原则 3: 上下文完整 — 每条日志必须包含 资源名 + 命名空间 + 操作描述 + 错误对象
```

### 1.3 适用范围

| 目录 | 适用 | 说明 |
|:---|:---:|:---|
| `controllers/` | ✅ | 控制器 Reconcile 逻辑 |
| `pkg/phaseframe/` | ✅ | Phase 执行框架 |
| `pkg/kube/` | ✅ | 远程集群客户端 |
| `pkg/command/` | ✅ | 命令执行引擎 |
| `utils/` | ✅ | 工具函数 |
| `api/` | ❌ | 仅类型定义，不含日志 |

---

## 2. 日志框架

### 2.1 短期方案：ologger 对齐规范（当前阶段）

当前使用自定义 `utils/log/log.go`（底层封装 `ologger`），在不更换框架的前提下，通过规范约束对齐 K8s 标准。

**V-level 映射表**：

| K8s 标准 | ologger 对应 | 使用方式 |
|:---|:---|:---|
| `V(0)` 始终可见 | `log.Info()` / `log.Infof()` | 直接调用 |
| `V(1)` 合理默认 | `log.Info()` / `log.Infof()` | 直接调用 |
| `V(2)` 推荐默认 | `log.Info()` / `log.Infof()` | 直接调用 |
| `V(3)` 扩展信息 | `log.Debug()` / `log.Debugf()` | 使用 Debug |
| `V(4)` 调试级别 | `log.Debug()` / `log.Debugf()` | 使用 Debug |
| `V(5)` 追踪级别 | `log.Trace()` / `log.Tracef()` | 使用 Trace |
| Error | `log.Error()` / `log.Errorf()` | 直接调用 |
| Warn | `log.Warn()` / `log.Warnf()` | 直接调用 |

> **注意**：ologger 的 `Debug` 对应 K8s 的 `V(3)~V(4)`，`Trace` 对应 `V(5)+`。生产环境应将日志级别设置为 Info，调试时切换为 Debug。

### 2.2 长期方案：logr 适配器（推荐迁移）

封装 logr 适配器，使现有代码渐进式迁移到 K8s 标准接口。

```go
// utils/log/logr_adapter.go

package log

import (
    "fmt"
    "strings"

    "github.com/go-logr/logr"
)

// Logger 封装 logr.Logger，兼容现有 ologger 调用方式
type Logger struct {
    inner logr.Logger
}

// NewLogger 从 logr.Logger 创建
func NewLogger(l logr.Logger) Logger {
    return Logger{inner: l}
}

// Info 结构化信息日志
func (l Logger) Info(msg string, keysAndValues ...interface{}) {
    l.inner.Info(msg, keysAndValues...)
}

// Infof 兼容旧调用方式（逐步废弃）
func (l Logger) Infof(format string, args ...interface{}) {
    l.inner.Info(fmt.Sprintf(format, args...))
}

// Error 错误日志（err 是第一个参数）
func (l Logger) Error(err error, msg string, keysAndValues ...interface{}) {
    l.inner.Error(err, msg, keysAndValues...)
}

// Errorf 兼容旧调用方式（逐步废弃）
func (l Logger) Errorf(format string, args ...interface{}) {
    l.inner.Error(nil, fmt.Sprintf(format, args...))
}

// V 返回指定详细度的 Logger
func (l Logger) V(level int) Logger {
    return Logger{inner: l.inner.V(level)}
}

// Debug 等价于 V(4).Info
func (l Logger) Debug(msg string, keysAndValues ...interface{}) {
    l.inner.V(4).Info(msg, keysAndValues...)
}

// Debugf 兼容旧调用方式（逐步废弃）
func (l Logger) Debugf(format string, args ...interface{}) {
    l.inner.V(4).Info(fmt.Sprintf(format, args...))
}

// WithValues 注入 key-value 对，后续所有日志自动携带
func (l Logger) WithValues(keysAndValues ...interface{}) Logger {
    return Logger{inner: l.inner.WithValues(keysAndValues...)}
}

// WithName 给 logger 命名（层级化）
func (l Logger) WithName(name string) Logger {
    return Logger{inner: l.inner.WithName(name)}
}

// buildMessage 将 key-value 对格式化为字符串（兼容 ologger 输出）
func buildMessage(msg string, keysAndValues ...interface{}) string {
    if len(keysAndValues) == 0 {
        return msg
    }
    var sb strings.Builder
    sb.WriteString(msg)
    for i := 0; i < len(keysAndValues)-1; i += 2 {
        sb.WriteString(fmt.Sprintf(" %v=%v", keysAndValues[i], keysAndValues[i+1]))
    }
    return sb.String()
}
```

**main() 入口配置**：

```go
// main.go
import (
    "github.com/go-logr/logr"
    "k8s.io/klog/v2"
    ctrl "sigs.k8s.io/controller-runtime"
)

func main() {
    // 方案 A：使用 klog（K8s 标准）
    klog.InitFlags(nil)
    ctrl.SetLogger(klog.NewKlogr())

    // 方案 B：使用 zap（高性能）
    // zapLogger, _ := zap.NewProduction()
    // ctrl.SetLogger(zapr.NewLogger(zapLogger))

    // 创建适配器
    logger := log.NewLogger(ctrl.Log)
    // ...
}
```

---

## 3. V-level 日志级别规范

### 3.1 六级定义

| 级别 | 语义 | 运行时可见条件 | 典型内容 |
|:---:|:---|:---|:---|
| **V(0)** | 始终可见 | 始终输出 | 编程错误、panic 附加信息、CLI 参数处理、升级开始/结束 |
| **V(1)** | 合理默认 | `--v>=1` | 配置信息（监听端口、监听资源）、可自愈的重复错误（Pod 不健康） |
| **V(2)** | **推荐默认** ⭐ | `--v>=2` | HTTP 请求及状态码、系统状态变更、控制器事件、调度器日志、Phase 执行状态 |
| **V(3)** | 扩展信息 | `--v>=3` (Debug) | 更详细的系统状态变更、DAG 批次详情 |
| **V(4)** | 调试级别 | `--v>=4` (Debug) | 复杂代码段调试、SSH 连接详情、OCI 解析过程 |
| **V(5)** | 追踪级别 | `--v>=5` (Trace) | 错误前上下文追踪、命令 stdout/stderr 输出、极细粒度排查 |

### 3.2 选择决策树

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

### 3.3 各级别典型场景

**V(0) — 始终可见**：
```go
log.Info("Reconciling BKECluster", "cluster", req.NamespacedName)
log.Info("Upgrade DAG execution completed", "totalComponents", 12)
log.Info("Cluster deletion started", "cluster", cluster.Name)
```

**V(2) — 推荐默认**：
```go
log.V(2).Info("Executing phase", "phase", "EnsureMasterInit")
log.V(2).Info("Processing DAG batch", "batch", 1, "totalBatches", 4)
log.V(2).Info("Component upgraded", "component", "etcd", "version", "v3.5.12")
```

**V(4) — 调试级别**：
```go
log.V(4).Info("Connecting to node via SSH", "node", node.IP, "port", 22)
log.V(4).Info("Resolving OCI manifest", "ref", "registry/release:v2.6.0")
log.V(4).Info("Failed to get resource (debug)", "name", name, "err", err)
```

**V(5) — 追踪级别**：
```go
log.V(5).Info("SSH command output", "node", node.IP, "stdout", result.Stdout, "exitCode", 0)
log.V(5).Info("Full reconcile context", "annotations", obj.Annotations, "finalizers", obj.Finalizers)
```

---

## 4. 结构化日志规范

### 4.1 消息风格规则

| 规则 | ✅ 正确 | ❌ 错误 |
|:---|:---|:---|
| 首字母大写 | `"Received HTTP request"` | `"received http request"` |
| 不以句号结尾 | `"Pod status updated"` | `"Pod status updated."` |
| 使用主动语态 | `"Could not delete pod"` | `"Pod could not be deleted"` |
| 使用过去时 | `"Could not delete pod"` | `"Cannot delete pod"` |
| 引用对象说明类型 | `"Deleted pod"` | `"Deleted"` |

### 4.2 Key 命名规范

| 规则 | ✅ 正确 | ❌ 错误 |
|:---|:---|:---|
| 人类可读 | `"pod"`, `"namespace"` | `"p"`, `"ns"` |
| 常量 key | `"cluster"`, `"version"` | 动态拼接 key |
| 全库一致 | 同一概念用同一 key | `"cluster"` / `"clusterName"` / `"c"` |
| 小写或 lowerCamelCase | `"nodeIP"`, `"exitCode"` | `"Node_IP"`, `"EXIT_CODE"` |
| key 与消息自然匹配 | msg=`"Pod updated"`, key=`"pod"` | msg=`"Updated"`, key=`"x"` |

### 4.3 结构化日志示例

```go
// ✅ 正确：结构化 key-value
log.Info("Pod status updated",
    "pod", "kube-system/kubedns",
    "status", "ready",
    "duration", time.Since(start))

// ❌ 错误：格式化字符串拼接
log.Infof("Pod %s status updated to %s in %v", podName, status, duration)
```

---

## 5. 错误处理规范

### 5.1 核心原则

> **"不要在返回错误之前打印 Error 日志"** — K8s SIG-Instrumentation
>
> 因为不确定调用者是否会优雅处理该错误。如果调用者处理了，打印 Error 日志会误导运维人员。

### 5.2 五种错误处理模式

#### 模式 1：可返回错误 → 不打印日志，用 fmt.Errorf 包装

```go
// ✅ 正确
func (r *Reconciler) getResource(ctx context.Context, name string) (*Resource, error) {
    obj := &Resource{}
    if err := r.Get(ctx, types.NamespacedName{Name: name}, obj); err != nil {
        return nil, fmt.Errorf("get resource %s: %w", name, err)
    }
    return obj, nil
}

// ❌ 错误：返回前打印 Error 日志
func (r *Reconciler) getResource(ctx context.Context, name string) (*Resource, error) {
    obj := &Resource{}
    if err := r.Get(ctx, types.NamespacedName{Name: name}, obj); err != nil {
        log.Errorf("failed to get resource %s: %v", name, err)  // ❌
        return nil, err
    }
    return obj, nil
}
```

#### 模式 2：调试用途 → V(4) + Info

```go
// ✅ 正确：仅调试时可见
func (r *Reconciler) getResource(ctx context.Context, name string) (*Resource, error) {
    obj := &Resource{}
    if err := r.Get(ctx, types.NamespacedName{Name: name}, obj); err != nil {
        log.V(4).Info("Failed to get resource", "name", name, "err", err)
        return nil, err
    }
    return obj, nil
}
```

#### 模式 3：调用链顶端（无法再向上返回）→ 用 Error

```go
// ✅ 正确：这里是调用链顶端，错误无法再向上返回
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    if err := r.doSomething(ctx); err != nil {
        log.Error(err, "Failed to reconcile", "resource", req.NamespacedName)
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }
    return ctrl.Result{}, nil
}
```

#### 模式 4：预期内的降级/跳过 → 用 Warn

```go
// ✅ 正确：预期内的跳过
if cluster.Spec.ExternallyManaged {
    log.Warn("Skipping externally managed cluster", "cluster", cluster.Name)
    return ctrl.Result{}, nil
}
```

#### 模式 5：绝对禁止 — 静默吞错

```go
// ❌ 绝对禁止：错误被吞没，既不记录也不返回
bkeCluster, err := mergecluster.GetCombinedBKECluster(ctx, r.Client, ns, name)
if err != nil {
    return ctrl.Result{}, nil  // ❌ 错误被吞没！控制器不会重试！
}

// ✅ 正确：返回错误，触发控制器重试
bkeCluster, err := mergecluster.GetCombinedBKECluster(ctx, r.Client, ns, name)
if err != nil {
    log.Error(err, "Failed to get combined BKECluster", "namespace", ns, "name", name)
    return ctrl.Result{}, err  // ✅ 返回错误
}
```

### 5.3 错误处理决策流程

```
发生错误
│
├── 能否向上返回 error？
│   ├── 是 → 模式 1: fmt.Errorf 包装返回（不打印日志）
│   │         如需调试 → 模式 2: V(4).Info 后返回
│   │
│   └── 否（调用链顶端）
│       ├── 是可自愈的预期错误 → 模式 4: Warn + 继续
│       └── 是需要关注的错误 → 模式 3: Error + 重试/退出
│
└── 绝对禁止：return nil（静默吞错）
```

---

## 6. 控制器日志规范

### 6.1 Reconcile 标准模板

```go
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 从 context 获取 logger（controller-runtime 自动注入 request 信息）
    log := ctrl.LoggerFrom(ctx)

    // 2. 入口日志 V(0)
    log.Info("Reconciling BKECluster")

    // 3. 获取资源（不打印日志，直接返回）
    cluster := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 4. 注入上下文（后续所有日志自动携带这些字段）
    log = log.WithValues("cluster", cluster.Name, "version", cluster.Spec.OpenFuyaoVersion)
    ctx = ctrl.LoggerIntoContext(ctx, log)

    // 5. 业务逻辑 V(1)
    log.V(1).Info("Processing cluster spec",
        "currentVersion", cluster.Status.CurrentVersion,
        "desiredVersion", cluster.Spec.DesiredVersion)

    // 6. 调用子函数
    if err := r.reconcilePhases(ctx, cluster); err != nil {
        // 7. 调用链顶端才用 Error
        log.Error(err, "Failed to reconcile phases")
        return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
    }

    // 8. 完成日志 V(0)
    log.Info("Reconciliation complete")
    return ctrl.Result{}, nil
}
```

### 6.2 具体 diff：`bkecluster_controller.go`

```go
// ❌ 整改前 (line 385)
bkeClusterLogger().Errorf("%T is externally managed, skipping mapping", providerCluster)

// ✅ 整改后
log.V(1).Info("Skipping externally managed cluster", "type", fmt.Sprintf("%T", providerCluster))
```

```go
// ❌ 整改前 (line 114) — 静默返回
oldBkeCluster, err := r.getOldBKECluster(bkeCluster)
if err != nil {
    return ctrl.Result{}, err
}

// ✅ 整改后
oldBkeCluster, err := r.getOldBKECluster(bkeCluster)
if err != nil {
    log.Error(err, "Failed to get old BKECluster snapshot", "cluster", bkeCluster.Name)
    return ctrl.Result{}, err
}
```

---

## 7. 共享库日志规范

### 7.1 核心原则

> **"共享库不应自行打印日志，而应只返回 error"** — K8s SIG-Instrumentation

共享库（`pkg/kube/`、`pkg/command/`、`utils/` 等）可能被多个调用方使用，自行打印日志会导致：
- 调用方无法控制日志输出
- 同一错误被重复打印
- 日志上下文缺失（库不知道调用方的业务上下文）

### 7.2 正确模式

```go
// ✅ pkg/kube/kube.go — 只返回 error
func NewRemoteClientByBKECluster(ctx context.Context, c client.Client, bc *bkev1beta1.BKECluster) (RemoteKubeClient, error) {
    client, err := tryKubeconfig(ctx, c, bc)
    if err != nil {
        // 不打印日志，包装错误返回
        return nil, fmt.Errorf("create remote client via kubeconfig for cluster %s/%s: %w",
            bc.Namespace, bc.Name, err)
    }
    return client, nil
}

// ✅ 调用方（控制器）决定是否打印日志
client, err := kube.NewRemoteClientByBKECluster(ctx, c, cluster)
if err != nil {
    log.Error(err, "Failed to create remote cluster client", "cluster", cluster.Name)
    return ctrl.Result{}, err
}
```

### 7.3 具体 diff：`pkg/kube/kube.go`

```go
// ❌ 整改前 — 共享库内部打印日志
func NewRemoteClusterClient(...) (RemoteKubeClient, error) {
    client, err := tryKubeconfig(...)
    if err != nil {
        log.Warnf("kubeconfig failed, trying token: %v", err)  // ❌ 库内打日志
        client, err = tryToken(...)
        if err != nil {
            log.Errorf("all methods failed: %v", err)  // ❌ 库内打日志
            return nil, err
        }
    }
    return client, nil
}

// ✅ 整改后 — 只返回 error，调用方决定
func NewRemoteClusterClient(...) (RemoteKubeClient, error) {
    var errs []error

    client, err := tryKubeconfig(...)
    if err != nil {
        errs = append(errs, fmt.Errorf("kubeconfig: %w", err))
    } else {
        return client, nil
    }

    client, err = tryToken(...)
    if err != nil {
        errs = append(errs, fmt.Errorf("token: %w", err))
    } else {
        return client, nil
    }

    return nil, fmt.Errorf("create remote client (tried %d methods): %w",
        len(errs), kerrors.NewAggregate(errs))
}
```

---

## 8. 对象引用规范

### 8.1 标准格式

| 对象类型 | 格式 | 示例 |
|:---|:---|:---|
| 命名空间对象 | `namespace/name` | `"kube-system/kubedns"` |
| 非命名空间对象 | `name` | `"node-pool-1"` |
| 多对象 | 分别列出 | `"pod": "ns/name", "node": "node-1"` |

### 8.2 辅助函数

```go
// utils/log/kobj.go

// KObj 返回命名空间对象的 "namespace/name" 格式
func KObj(namespace, name string) string {
    if namespace == "" {
        return name
    }
    return namespace + "/" + name
}

// KObjFromMeta 从 ObjectMeta 获取引用
func KObjFromMeta(obj metav1.ObjectMeta) string {
    return KObj(obj.Namespace, obj.Name)
}
```

### 8.3 具体 diff：`bkemachine_controller_phases.go`

```go
// ❌ 整改前 (line 192, 491, 834, 852) — 脚手架残留 "demoMachine"
params.Log.Error("failed to patch demoMachine", err)

// ✅ 整改后
params.Log.Error(err, "Failed to patch BKEMachine",
    "bkemachine", log.KObjFromMeta(params.BKEMachine.ObjectMeta))
```

---

## 9. Phase 日志规范

### 9.1 四类必须日志

| 类型 | 级别 | 时机 | 示例 |
|:---|:---:|:---|:---|
| **入口日志** | V(0) | Phase.Execute() 开始 | `"Starting phase", "phase", "EnsureMasterInit"` |
| **出口日志** | V(0) | Phase.Execute() 结束 | `"Phase completed", "phase", "EnsureMasterInit", "duration", "5s"` |
| **状态变更** | V(2) | 关键状态转换 | `"Node marked as upgrading", "node", "192.168.1.1"` |
| **外部操作** | V(2)/V(4) | SSH/API/远程调用 | 入口 V(2) + 详情 V(4) |

### 9.2 Phase 标准模板

```go
func (p *EnsureMasterInit) Execute() (ctrl.Result, error) {
    log := p.Ctx.Log

    // 入口日志 V(0)
    log.Info("Starting phase", "phase", "EnsureMasterInit")
    start := time.Now()

    // 业务逻辑...
    log.V(2).Info("Initializing control plane", "node", masterNode.IP)

    // 外部操作：入口 V(2) + 详情 V(4)
    log.V(2).Info("Executing SSH command on nodes", "nodeCount", len(nodes))
    log.V(4).Info("SSH command details", "command", cmd, "nodes", nodeIPs)

    // 出口日志 V(0)
    log.Info("Phase completed",
        "phase", "EnsureMasterInit",
        "duration", time.Since(start).String())

    return ctrl.Result{}, nil
}
```

### 9.3 具体 diff

```go
// ❌ 整改前 — phase_flow.go:216 循环内 Info 刷屏
p.ctx.Log.NormalLogger.Infof("current phase name: %s", phase.Name())

// ✅ 整改后 — 改为 Debug
p.ctx.Log.NormalLogger.Debugf("Processing phase: %s", phase.Name())
```

```go
// ❌ 整改前 — ensure_cluster.go:116 nil error 打印
log.Error("isClusterInSpecialState func err is %s", err.Error())  // err 实际为 nil

// ✅ 整改后 — 删除此行（err 为 nil，此行为 Bug）
// （直接删除）
```

---

## 10. 禁止事项清单

以下 8 条为**红线**，代码审查中发现即打回：

| # | 禁止事项 | 示例 | 正确做法 |
|:---:|:---|:---|:---|
| 1 | **静默吞错** | `if err != nil { return ctrl.Result{}, nil }` | 返回 err 或记录后处理 |
| 2 | **返回前打 Error** | `if err != nil { log.Error(err); return err }` | `fmt.Errorf` 包装返回 |
| 3 | **Info 打失败信息** | `log.Infof("failed to generate cert: %v", err)` | `log.Errorf` 或 `return fmt.Errorf` |
| 4 | **Error 打正常信息** | `log.Errorf("externally managed, skipping")` | `log.Info` 或 `log.V(1).Info` |
| 5 | **循环内 Info 刷屏** | 循环中 `log.Info("checking node...")` | `log.Debug` 或 `log.V(4).Info` |
| 6 | **缺失上下文** | `log.Error("failed")` | `log.Error(err, "msg", "resource", name)` |
| 7 | **脚手架残留** | `"failed to patch demoMachine"` | 替换为实际资源名 |
| 8 | **共享库自行打日志** | `pkg/kube/` 内 `log.Errorf(...)` | 只返回 error |

### 具体 diff 汇总

```go
// ❌ #3: kubeconfig.go:253 — Info 打失败
log.Infof("failed to generate new cert and key for %q: %v", certSpec.Name, err)
// ✅ 整改后
return fmt.Errorf("generate cert and key for %q: %w", certSpec.Name, err)

// ❌ #3: ensure_worker_delete.go:377 — Info 打失败
params.Log.Info(constant.WorkerDeleteFailedReason, "Some nodes cannot be completely deleted")
// ✅ 整改后
params.Log.Error(nil, "Some nodes cannot be completely deleted",
    "failedNodes", failedNodes, "phase", "EnsureWorkerDelete")

// ❌ #4: bkecluster_controller.go:385 — Error 打正常跳过
bkeClusterLogger().Errorf("%T is externally managed, skipping mapping", providerCluster)
// ✅ 整改后
log.V(1).Info("Skipping externally managed cluster", "type", fmt.Sprintf("%T", providerCluster))

// ❌ #5: phase_flow.go:216 — 循环内 Info
p.ctx.Log.NormalLogger.Infof("current phase name: %s", phase.Name())
// ✅ 整改后
p.ctx.Log.NormalLogger.Debugf("Processing phase: %s", phase.Name())

// ❌ #8: bkecluster.go:134 — 参数顺序错误
log.Error(cmd.Name, err)
// ✅ 整改后
log.Error(err, "Command validation failed", "command", cmd.Name)
```

---

## 11. 迁移指南

### 11.1 分阶段路线

```
Phase 0 (P0 紧急, 1-2 人天)     Phase 1 (P1 高, 3-5 人天)
━━━━━━━━━━━━━━━━━━━━━━━━━━━     ━━━━━━━━━━━━━━━━━━━━━━━━━━
• 修复错误吞没 Bug               • 日志级别纠正 (Info↔Error)
• 清理 "demoMachine" 残留        • 错误前不打 Error
• 删除 nil error 日志            • 对象引用规范化

Phase 2 (P2 中, 5-7 人天)       Phase 3 (P3 低, 7-10 人天)
━━━━━━━━━━━━━━━━━━━━━━━━━━━     ━━━━━━━━━━━━━━━━━━━━━━━━━━
• 封装 logr 适配器               • JSON 输出支持
• V-level 引入                   • context 传递
• WithValues 上下文              • 日志 schema 校验
```

### 11.2 旧→新对照表

| 旧代码 | 新代码 | 说明 |
|:---|:---|:---|
| `log.Infof("msg %s %v", a, err)` | `log.Error(err, "msg", "key", a)` | 结构化 + 级别纠正 |
| `log.Errorf("skipping %s", name)` | `log.V(1).Info("Skipping", "name", name)` | 正常逻辑不用 Error |
| `log.Info("current phase: %s", p)` | `log.Debug("Processing phase", "phase", p)` | 循环内降噪 |
| `if err != nil { return nil }` | `if err != nil { return err }` | 禁止吞错 |
| `if err != nil { log.Error(err); return err }` | `return fmt.Errorf("...: %w", err)` | 返回前不打 Error |
| `log.Error("failed to patch demoMachine", err)` | `log.Error(err, "Failed to patch BKEMachine", "bkemachine", KObj(ns, name))` | 修复残留 + 结构化 |
| `log.Error(cmd.Name, err)` | `log.Error(err, "Command failed", "command", cmd.Name)` | 修正参数顺序 |
| `log.Error("some err in file.go: %s", err)` | `log.Error(err, "Failed to ensure cluster health", "cluster", name)` | 描述操作而非文件名 |

### 11.3 按文件分组的整改清单

| 文件 | 行号 | 问题 | 优先级 |
|:---|:---:|:---|:---:|
| `controllers/capbke/bkemachine_controller.go` | 124 | 错误吞没 `return nil` | P0 |
| `controllers/capbke/bkemachine_controller_phases.go` | 192,491,834,852 | `"demoMachine"` 残留 | P0 |
| `pkg/phaseframe/phases/ensure_cluster.go` | 116 | nil error 打印 | P0 |
| `utils/bkeagent/pkiutil/kubeconfig.go` | 253 | Infof 打失败 | P1 |
| `controllers/capbke/bkecluster_controller.go` | 385 | Errorf 打正常跳过 | P1 |
| `pkg/phaseframe/phases/phase_flow.go` | 216 | 循环内 Info 刷屏 | P1 |
| `pkg/phaseframe/phases/ensure_worker_delete.go` | 377 | Info 打失败 | P1 |
| `pkg/phaseframe/phaseutil/bkecluster.go` | 134 | 参数顺序错误 | P1 |
| `pkg/phaseframe/phases/ensure_master_upgrade.go` | 66 | 静默返回 | P1 |
| `controllers/capbke/bkecluster_controller.go` | 114 | 静默返回 | P1 |
| `controllers/capbke/bkemachine_controller.go` | 104,211 | 静默返回 | P1 |
| `pkg/phaseframe/phases/phase_flow.go` | 192-203 | defer 中静默吞错 | P1 |
| `controllers/capbke/bkemachine_controller.go` | 583-589 | 错误完全忽略 | P1 |

---

## 12. 速查表

```
┌─────────────────────────────────────────────────────────────────────┐
│                    openFuyao 日志规范速查表                           │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  三条原则                                                           │
│  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━      │
│  1. 结构化 — key-value，禁止拼接                                    │
│  2. 级别准确 — 失败 Error，正常 Info，调试 Debug                     │
│  3. 上下文完整 — 资源名 + 命名空间 + 操作 + 错误                     │
│                                                                     │
│  V-level 速查                                                       │
│  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━      │
│  V(0) 始终可见    升级开始/结束、严重状态变更                         │
│  V(2) 推荐默认 ⭐  控制器事件、Phase 状态、HTTP 请求                  │
│  V(4) 调试级别    SSH 详情、OCI 解析、复杂代码段                     │
│                                                                     │
│  错误处理速查                                                       │
│  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━      │
│  能返回 → fmt.Errorf("...: %w", err)  不打印日志                    │
│  不能返回 → log.Error(err, "msg", k, v) 调用链顶端才用              │
│  调试用途 → log.V(4).Info("msg", "err", err)                       │
│  绝对禁止 → return nil（静默吞错）                                   │
│                                                                     │
│  八条红线                                                           │
│  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━      │
│  ✗ 静默吞错  ✗ 返回前打 Error  ✗ Info 打失败  ✗ Error 打正常        │
│  ✗ 循环内 Info  ✗ 缺失上下文  ✗ 脚手架残留  ✗ 共享库自行打日志      │
│                                                                     │
│  消息风格                                                           │
│  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━      │
│  ✓ 首字母大写  ✓ 不以句号结尾  ✓ 主动语态  ✓ 过去时  ✓ 说明对象类型  │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

> **文档维护**：本规范由 openFuyao 架构组维护，修订需经架构评审通过。  
> **代码审查**：所有 PR 必须通过日志规范检查，违反红线即打回。  
> **问题反馈**：发现规范未覆盖的场景，请提交 Issue 至 `cluster-api-provider-bke` 仓库。

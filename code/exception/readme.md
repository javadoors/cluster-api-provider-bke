# 状态机重构需求
基于对 `code/exception` 目录下状态机重构文档的深入分析，以下是状态机重构需求的一句话描述：

## 状态机重构需求一句话描述

> **将分散在 11+ 个硬编码函数中的状态转换逻辑收敛为声明式状态转换表，同时解决全局单例内存泄漏、固定重试策略、并发安全隐患三大生产风险，使状态转换可验证、可观测、可追溯。**

如需更精简的汇报版本：

| 场景 | 一句话描述 |
|------|-----------|
| **技术汇报** | 状态转换逻辑从"散落在 11 个 switch-case 函数"重构为"集中式状态转换表 + 事件系统"，消除内存泄漏与并发安全隐患 |
| **领导汇报** | 集群状态管理从"黑盒"变为"白盒"：所有状态转换规则一张表定义，失败自动重试带退避，历史可追溯可导出 |
| **立项申请** | 解决 3 个高风险生产问题（内存泄漏/并发死锁/重试僵化）+ 2 个可维护性问题（状态逻辑分散/不可观测），降低故障排查时间 90% |

# 基于 `code/exception/状态机重构.md` 及源码分析，以下是 5 个问题的详细解释：

## 一、3 个高风险生产问题

### 1. 内存泄漏 — 全局单例 StatusManager 无限增长

**问题根源**：
```go
// pkg/statusmanage/statusmanager.go
var BKEClusterStatusManager = NewStatusManager()  // 全局单例，进程生命周期存在

type StatusManager struct {
    BKEClusterStatusMap map[string]*StatusRecord  // key: "namespace/name"
    BKENodesStatusMap   map[string]map[string]*StatusRecord
}
```
**泄漏路径**：
```
集群创建 → 写入 BKEClusterStatusMap["default/cluster-1"] = &StatusRecord{...}
集群删除 → ❌ 没有清理逻辑，记录永远留在 Map 中
新集群创建 → 写入新记录
...
运行 1 年后 → Map 中积累数千个已删除集群的僵尸记录
```

**生产影响**：

| 时间 | 集群数量 | 内存占用估算 | 风险 |
|------|---------|-------------|------|
| 上线 1 个月 | 500 | ~5MB | 无感 |
| 上线 6 个月 | 3000 | ~30MB | GC 压力增大 |
| 上线 1 年 | 10000+ | ~100MB+ | 管理集群 OOM，所有控制器崩溃 |

**缺失机制**：
- 无集群删除时的记录清理
- 无过期时间 (TTL) 机制
- 无定期清理 (GC) 协程

### 2. 并发死锁 — 持有锁时修改外部对象

**问题代码**：
```go
func (b *StatusManager) recordBKEClusterStatus(bkeCluster *BKECluster) {
    b.cmux.Lock()          // ← 获取写锁
    defer b.cmux.Unlock()

    sr := b.BKEClusterStatusMap[key]
    if sr.AllowFailed() {
        bkeCluster.Status.ClusterStatus = confv1beta1.ClusterStatus(sr.LatestNormalState)
        // ↑ 在持有锁的情况下修改外部 BKECluster 对象
        // 如果其他地方也在尝试获取锁并读取该对象 → 死锁风险
    }
}
```

**死锁场景推演**：
```
Goroutine A (Phase 执行):                    Goroutine B (状态监控):
────────────────────────                     ────────────────────────
b.cmux.Lock()                                b.cmux.Lock() ← 阻塞等待
  ↓
修改 bkeCluster.Status
  ↓
触发 SyncStatusUntilComplete()
  ↓
  内部可能触发其他读操作...                      等待 A 释放锁
```

**竞态条件**：
```go
// 状态恢复逻辑存在 TOCTOU (Time-of-Check-Time-of-Use) 问题
if sr.AllowFailed() {
    bkeCluster.Status.ClusterStatus = sr.LatestNormalState  // ← 读取 LatestNormalState
    // 此时另一个 goroutine 可能已经修改了 LatestNormalState
    sr.NeedRequeue = true
}
```

### 3. 重试僵化 — 所有 Phase 共用固定失败次数

**问题代码**：
```go
const DefaultAllowedFailedCount = 10  // 全局固定值

func (sr *StatusRecord) AllowFailed() bool {
    return sr.StatusCount < ReconcileAllowedFailedCount
}
```
**不合理之处**：

| Phase | 失败原因 | 合理重试次数 | 当前策略 | 问题 |
|-------|---------|-------------|---------|------|
| EnsureMasterInit | 节点网络不通 | 3 次后应快速失败 | 10 次 | 无效重试 30 分钟 |
| EnsureWorkerJoin | 节点引导中，等待就绪 | 20 次也合理 | 10 次 | 过早放弃，集群不完整 |
| EnsureEtcdUpgrade | 数据迁移中 | 5 次 + 指数退避 | 10 次线性 | 频繁重试加重负载 |
| EnsureAgentUpgrade | Agent 镜像拉取 | 5 次 + 线性退避 | 10 次线性 | 浪费 API Server 资源 |

**缺失机制**：
- 无 Phase 级别重试策略配置
- 无指数退避 (Exponential Backoff)
- 无最大重试时间限制
- 重试间隔固定，无抖动 (Jitter)

**生产事故场景**：
```
EnsureMasterInit 失败 (节点网络故障)
→ 立即重试 (无退避)
→ 再次失败，立即重试
→ 10 次重试在 2 分钟内完成
→ 最终标记失败，但此时网络可能已恢复
→ 用户看到 "InitializationFailed"，实际再等 5 分钟就能成功
```

## 二、2 个可维护性问题

### 4. 状态逻辑分散 — 11 个 switch-case 函数散落在代码中

**当前架构**：
```
状态转换逻辑分散在 3 个层级、4 个文件中：

┌─────────────────────────────────────────────────────────────────┐
│  层级 1: phase_flow.go (状态转换主函数)                          │
│  calculateClusterStatusByPhase()                                │
│    switch {                                                     │
│    case phaseName.In(ClusterInitPhaseNames):                    │
│        handleClusterInitPhase(ctx, err)        ← 函数 1          │
│    case phaseName.In(ClusterUpgradePhaseNames):                 │
│        handleClusterUpgradePhase(ctx, err)     ← 函数 2          │
│    case phaseName.In(ClusterScaleMasterUpPhaseNames):           │
│        handleClusterScaleMasterUpPhase(ctx, err) ← 函数 3        │
│    case phaseName.In(ClusterScaleMasterDownPhaseNames):         │
│        handleClusterScaleMasterDownPhase(ctx, err) ← 函数 4      │
│    case phaseName.In(ClusterScaleWorkerUpPhaseNames):           │
│        handleClusterScaleWorkerUpPhase(ctx, err) ← 函数 5        │
│    case phaseName.In(ClusterScaleWorkerDownPhaseNames):         │
│        handleClusterScaleWorkerDownPhase(ctx, err) ← 函数 6      │
│    case phaseName.In(ClusterDeletePhaseNames):                  │
│        handleClusterDeletePhase(ctx, err)      ← 函数 7          │
│    case phaseName.In(ClusterPausedPhaseNames):                  │
│        handleClusterPausedPhase(ctx, err)      ← 函数 8          │
│    case phaseName.In(ClusterDryRunPhaseNames):                  │
│        handleClusterDryRunPhase(ctx, err)      ← 函数 9          │
│    case phaseName.In(ClusterAddonsPhaseNames):                  │
│        handleClusterAddonsPhase(ctx, err)      ← 函数 10         │
│    case phaseName.In(ClusterManagePhaseNames):                  │
│        handleClusterManagePhase(ctx, err)      ← 函数 11         │
│    default:                                                     │
│        ctx.BKECluster.Status.ClusterStatus = ClusterUnknown     │
│    }                                                            │
├─────────────────────────────────────────────────────────────────┤
│  层级 2: list.go (Phase 分组定义)                                │
│    ClusterInitPhaseNames = [...]          ← 与 switch case 1 对应 │
│    ClusterUpgradePhaseNames = [...]       ← 与 switch case 2 对应 │
│    ... (11 个分组)                                              │
├─────────────────────────────────────────────────────────────────┤
│  层级 3: statusmanager.go (失败重试逻辑)                         │
│    recordBKEClusterStatus()               ← 状态恢复逻辑          │
│    handleFailure()                        ← 失败处理逻辑          │
├─────────────────────────────────────────────────────────────────┤
│  层级 4: 各 Phase 内部 (NeedExecute)                            │
│    每个 Phase 自行判断是否需要执行                               │
│    26 个 Phase × 8+ 个条件判断 = 200+ 行分散逻辑                 │
└─────────────────────────────────────────────────────────────────┘
```

**新增一个状态需要修改的地方**：
```
场景: 新增 "集群备份中 (BackingUp)" 状态

需要修改:
  ① bkecluster_consts.go: 添加 ClusterBackingUp / ClusterBackupFailed 常量
  ② list.go: 添加 ClusterBackupPhaseNames = [EnsureBackupName]
  ③ phase_flow.go: 添加 case phaseName.In(ClusterBackupPhaseNames): handleClusterBackupPhase()
  ④ phase_flow.go: 实现 handleClusterBackupPhase() 函数
  ⑤ statusmanager.go: 如果需要特殊重试逻辑，修改 handleFailure()
  ⑥ PhaseNameCNMap: 添加中文映射 "备份中"
  ⑦ 测试: 修改所有涉及状态转换的单元测试

影响: 6+ 个文件，新人不敢改，老人容易漏改
```

### 5. 不可观测 — 状态转换无事件记录，故障排查靠猜

**当前缺失**：
```
┌─────────────────────────────────────────────────────────────────┐
│  缺失 1: 无状态转换事件日志                                      │
│  ─────────────────────────                                      │
│  无法回答:                                                       │
│  • 集群从 Initializing → Ready 经历了多少次调谐？                │
│  • 哪个 Phase 导致了 UpgradeFailed？                            │
│  • 失败重试了多少次？每次间隔多久？                              │
│  • 状态转换的时间线是什么？                                      │
│                                                                 │
│  当前只能:                                                       │
│  • 看 controller 日志 (如果没开 debug 级别，看不到)               │
│  • 看 BKECluster.Status.PhaseStatus (最多 20 条，旧的被丢弃)      │
│  • 猜                                                          │
├─────────────────────────────────────────────────────────────────┤
│  缺失 2: 无状态转换历史归档                                      │
│  ─────────────────────                                          │
│  PhaseStatus 清理逻辑:                                           │
│    if len(PhaseStatus) > 20 {                                    │
│        PhaseStatus = PhaseStatus[len-20:]  ← 直接截断丢弃        │
│    }                                                             │
│                                                                 │
│  问题:                                                           │
│  • 运行 3 个月的集群，升级失败了，想看第一次升级尝试的记录 → 没了  │
│  • 无法生成升级报告                                              │
│  • 无法做根因分析 (RCA)                                          │
├─────────────────────────────────────────────────────────────────┤
│  缺失 3: 无状态机可视化                                          │
│  ──────────────────                                             │
│  无法生成:                                                       │
│  • 状态转换图 (哪些状态经常转换，哪些是死胡同)                     │
│  • 状态转换频率统计                                              │
│  • 失败热点分析 (哪个 Phase 最常失败)                            │
│                                                                 │
│  对比: 如果有事件系统，可以导出 Graphviz DOT 格式:               │
│    digraph StateMachine {                                       │
│      "Initializing" -> "Ready" [label="156次"];                 │
│      "Initializing" -> "InitializationFailed" [label="12次"];    │
│      "Upgrading" -> "UpgradeFailed" [label="8次", color=red];    │
│    }                                                             │
├─────────────────────────────────────────────────────────────────┤
│  缺失 4: 无状态转换验证                                          │
│  ───────────────────                                            │
│  无法阻止非法转换:                                               │
│  • Initializing → Deleting (初始化中直接删除？)                  │
│  • Ready → Upgrading (但版本没变？)                              │
│  • 任何状态 → Ready (但控制面实际未就绪？)                        │
│                                                                 │
│  当前: 状态转换无前置条件校验，完全信任代码逻辑                    │
└─────────────────────────────────────────────────────────────────┘
```
**故障排查对比**：

| 场景 | 当前方式 | 重构后方式 |
|------|---------|-----------|
| 升级失败排查 | 翻 controller 日志 → 猜哪个 Phase 失败 → 看 PhaseStatus 最后一条 | `kubectl get events --field-selector reason=StateTransition` → 完整时间线 |
| 根因分析 | 凭经验 + 问开发 | 导出事件历史 → 生成状态转换图 → 一眼看到失败路径 |
| 性能分析 | 无数据 | 每个事件带 Duration 字段，统计各 Phase 耗时 |
| 审计合规 | 无记录 | 所有状态转换持久化为 K8s Event，可保留 1 年 |

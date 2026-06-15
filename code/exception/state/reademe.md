# 状态机重构方案：基于 CVO + DAG 调度的声明式状态管理

## 一、现状问题分析

### 1.1 当前架构核心问题

| 问题 | 现状 | 影响 |
|------|------|------|
| **线性执行** | 26个Phase按注册顺序串行执行 | 无法并行，升级耗时长 |
| **依赖隐式** | Phase N 依赖 Phase N-1 完成，无显式声明 | 新人无法理解依赖关系 |
| **双轨并行** | PhaseFlow (旧) + DAG (新) 共存，通过 `skipPhaseAfterDeclarativeDAG()` 避免重复 | 代码复杂度高，维护困难 |
| **状态转换散乱** | 11个 `handleCluster*Phase()` 函数分散在 `phase_flow.go` | 新增Phase需修改多处 |
| **StatusManager内存泄漏** | 全局单例 `BKEClusterStatusMap` 无限增长 | 长期运行OOM风险 |
| **无统一状态机引擎** | 状态转换靠硬编码 switch-case | 无法验证状态转换合法性 |

### 1.2 当前执行流程

```
BKEClusterReconciler.Reconcile()
  │
  ├── shouldUseDeclarativeUpgrade()? ──是──▶ executeUpgradeDAG() ──▶ 标记DAG完成
  │                                              │
  │                                              └──▶ PhaseFlow 跳过已执行的升级Phase
  │
  └── PhaseFlow (旧路径)
       ├── CalculatePhase() ──▶ 确定需要执行的Phase列表
       └── Execute() ──▶ 串行执行每个Phase
            ├── PreHook ──▶ 设置状态为Running
            ├── Execute() ──▶ 执行业务逻辑
            └── PostHook ──▶ 设置状态为Succeeded/Failed
                 └── calculateClusterStatusByPhase() ──▶ 11个switch-case分支
```

## 二、目标架构

### 2.1 架构设计原则

1. **统一调度引擎**：PhaseFlow 和 UpgradeDAG 合并为统一的 DAG 调度器
2. **声明式依赖**：Phase 依赖关系通过 DAG 显式声明，而非隐式顺序
3. **状态机引擎**：引入统一的状态转换表，替代 11 个 switch-case 函数
4. **CVO 模式**：ClusterVersion 作为版本声明入口，触发 DAG 执行
5. **并行执行**：无依赖的 Phase 可并行执行

### 2.2 目标架构图

```
┌─────────────────────────────────────────────────────────────────┐
│                        BKECluster                                │
│  (集群实例，生命周期管理)                                         │
└──────────────────────────┬──────────────────────────────────────┘
                           │ 1:1 OwnerReference
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                      ClusterVersion                              │
│  spec.desiredVersion: v2.6.0                                     │
│  status.currentVersion: v2.5.0                                   │
│  status.phase: Progressing                                       │
│  status.conditions: [...]                                        │
└──────────────────────────┬───────────────────────────────────────┘
                           │ 触发升级
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                    DAG Scheduler (统一调度器)                      │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  State Machine Engine (状态机引擎)                          │  │
│  │  ┌──────────────────────────────────────────────────────┐  │  │
│  │  │  StateTransitionTable (状态转换表)                    │  │  │
│  │  │  {from, to, trigger, condition, action}              │  │  │
│  │  └──────────────────────────────────────────────────────┘  │  │
│  │  ┌──────────────────────────────────────────────────────┐  │  │
│  │  │  StateValidator (状态验证器)                          │  │  │
│  │  │  验证状态转换合法性，防止非法转换                      │  │  │
│  │  └──────────────────────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  DAG Graph (DAG图模型)                                     │  │
│  │  ┌──────────┐    ┌──────────┐    ┌──────────┐             │  │
│  │  │ Phase A  │───▶│ Phase B  │───▶│ Phase D  │             │  │
│  │  └──────────┘    └──────────┘    └──────────┘             │  │
│  │       │              │                                     │  │
│  │       │         ┌──────────┐    ┌──────────┐             │  │
│  │       └────────▶│ Phase C  │───▶│ Phase E  │             │  │
│  │                 └──────────┘    └──────────┘             │  │
│  │                                                          │  │
│  │  • 拓扑排序 → 分批执行                                    │  │
│  │  • 批次内并行执行                                         │  │
│  │  • 失败策略: FailFast / Continue / Rollback              │  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  Event Recorder (事件记录器)                                │  │
│  │  • 记录所有状态转换事件                                     │  │
│  │  • 支持查询和导出                                           │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

## 三、详细设计方案

### 3.1 统一 DAG 调度器

#### 3.1.1 DAG 图模型

```go
// pkg/dagexec/graph.go

// DAG 表示Phase执行图
type DAG struct {
    nodes map[string]*Node
    edges map[string][]string  // nodeID -> [dependentNodeIDs]
}

// Node 表示DAG中的一个节点
type Node struct {
    ID          string
    Phase       phaseframe.Phase
    Dependencies []string     // 依赖的Phase ID列表
    FailurePolicy FailurePolicy  // FailFast / Continue / Rollback
    Timeout     time.Duration
    RetryPolicy *RetryPolicy
}

// FailurePolicy 定义失败策略
type FailurePolicy string

const (
    FailFast   FailurePolicy = "FailFast"    // 立即终止
    Continue   FailurePolicy = "Continue"    // 继续执行无依赖节点
    Rollback   FailurePolicy = "Rollback"    // 回滚后继续
)

// TopologicalBatches 返回拓扑排序后的执行批次
func (d *DAG) TopologicalBatches() [][]*Node {
    // Kahn's algorithm 实现
    // 返回: [[batch1], [batch2], ...]
    // 批次间串行，批次内并行
}
```

#### 3.1.2 安装 DAG 定义

```go
// pkg/dagexec/install_dag.go

func BuildInstallDAG() *DAG {
    dag := NewDAG()
    
    // CommonPhases (无依赖，可并行)
    dag.AddNode("finalizer", NewEnsureFinalizer(), nil, FailFast)
    dag.AddNode("paused", NewEnsurePaused(), []string{"finalizer"}, Continue)
    dag.AddNode("manage", NewEnsureClusterManage(), []string{"paused"}, Continue)
    dag.AddNode("delete", NewEnsureDeleteOrReset(), []string{"manage"}, Continue)
    dag.AddNode("dryrun", NewEnsureDryRun(), []string{"delete"}, Continue)
    
    // DeployPhases (有依赖关系)
    dag.AddNode("agent", NewEnsureBKEAgent(), []string{"dryrun"}, FailFast)
    dag.AddNode("env", NewEnsureNodesEnv(), []string{"agent"}, FailFast)
    dag.AddNode("apiobj", NewEnsureClusterAPIObj(), []string{"env"}, FailFast)
    dag.AddNode("certs", NewEnsureCerts(), []string{"apiobj"}, FailFast)
    dag.AddNode("lb", NewEnsureLoadBalance(), []string{"certs"}, Continue)
    dag.AddNode("master_init", NewEnsureMasterInit(), []string{"lb"}, FailFast)
    dag.AddNode("master_join", NewEnsureMasterJoin(), []string{"master_init"}, Continue)
    dag.AddNode("worker_join", NewEnsureWorkerJoin(), []string{"master_init"}, Continue)
    dag.AddNode("addon", NewEnsureAddonDeploy(), []string{"worker_join", "master_join"}, Continue)
    dag.AddNode("postprocess", NewEnsureNodesPostProcess(), []string{"addon"}, Continue)
    dag.AddNode("agent_switch", NewEnsureAgentSwitch(), []string{"postprocess"}, Continue)
    
    return dag
}
```

#### 3.1.3 升级 DAG 定义

```go
// pkg/dagexec/upgrade_dag.go

func BuildUpgradeDAG() *DAG {
    dag := NewDAG()
    
    // 升级Phase (按依赖关系组织)
    dag.AddNode("provider", NewEnsureProviderSelfUpgrade(), nil, Continue)
    dag.AddNode("agent", NewEnsureAgentUpgrade(), []string{"provider"}, Continue)
    dag.AddNode("containerd", NewEnsureContainerdUpgrade(), []string{"agent"}, Continue)
    dag.AddNode("etcd", NewEnsureEtcdUpgrade(), []string{"containerd"}, FailFast)
    dag.AddNode("worker", NewEnsureWorkerUpgrade(), []string{"etcd"}, Continue)
    dag.AddNode("master", NewEnsureMasterUpgrade(), []string{"etcd"}, FailFast)
    dag.AddNode("worker_del", NewEnsureWorkerDelete(), []string{"worker", "master"}, Continue)
    dag.AddNode("master_del", NewEnsureMasterDelete(), []string{"worker", "master"}, FailFast)
    dag.AddNode("component", NewEnsureComponentUpgrade(), []string{"worker_del", "master_del"}, Continue)
    dag.AddNode("cluster", NewEnsureCluster(), []string{"component"}, Continue)
    
    return dag
}
```

#### 3.1.4 调度器执行逻辑

```go
// pkg/dagexec/scheduler.go

type Scheduler struct {
    config Config
}

type Config struct {
    MaxParallel    int           // 批次内最大并行数
    DefaultTimeout time.Duration // 默认超时
    InlineRunner   *componentfactory.PhaseRunner
    ManifestStore  *manifest.Store
    ManifestApplier *manifest.Applier
}

func (s *Scheduler) ExecuteDAG(ctx context.Context, dag *DAG) error {
    batches := dag.TopologicalBatches()
    
    for _, batch := range batches {
        // 批次内并行执行
        if err := s.executeBatchParallel(ctx, batch); err != nil {
            // 根据FailurePolicy决定后续行为
            if err.Policy == FailFast {
                return err
            }
            // Continue: 记录错误，继续下一批次
            s.recordError(err)
        }
    }
    
    return nil
}

func (s *Scheduler) executeBatchParallel(ctx context.Context, batch []*Node) error {
    var g errgroup.Group
    g.SetLimit(s.config.MaxParallel)
    
    for _, node := range batch {
        node := node // capture
        g.Go(func() error {
            return s.executeNode(ctx, node)
        })
    }
    
    return g.Wait()
}

func (s *Scheduler) executeNode(ctx context.Context, node *Node) error {
    // 1. 检查依赖是否完成
    if !s.areDependenciesComplete(node) {
        return &DependencyError{Node: node.ID}
    }
    
    // 2. 检查是否需要执行
    if !node.Phase.NeedExecute(old, new) {
        node.Phase.SetStatus(PhaseSkipped)
        return nil
    }
    
    // 3. 执行Phase (带超时和重试)
    ctx, cancel := context.WithTimeout(ctx, node.Timeout)
    defer cancel()
    
    return s.executeWithRetry(ctx, node)
}
```

### 3.2 状态机引擎

#### 3.2.1 状态转换表

```go
// pkg/statemachine/transition.go

// StateTransition 定义一个状态转换规则
type StateTransition struct {
    From      ClusterStatus
    To        ClusterStatus
    Trigger   string           // 触发条件 (Phase名称或事件)
    Condition func(*BKECluster) bool  // 转换条件
    Action    func(*BKECluster) error // 转换动作
}

// 状态转换表
var ClusterStateTransitionTable = []StateTransition{
    // 初始化阶段
    {ClusterUnknown, ClusterInitializing, "EnsureFinalizer", nil, nil},
    {ClusterInitializing, ClusterReady, "EnsureCluster", isClusterReady, nil},
    {ClusterInitializing, ClusterInitializationFailed, "Error", nil, nil},
    
    // 升级阶段
    {ClusterReady, ClusterUpgrading, "EnsureAgentUpgrade", needUpgrade, nil},
    {ClusterUpgrading, ClusterReady, "EnsureCluster", isUpgradeComplete, nil},
    {ClusterUpgrading, ClusterUpgradeFailed, "Error", nil, nil},
    
    // 扩缩容阶段
    {ClusterReady, ClusterMasterScalingUp, "EnsureMasterJoin", needMasterScaleUp, nil},
    {ClusterMasterScalingUp, ClusterReady, "EnsureCluster", isScaleComplete, nil},
    {ClusterMasterScalingUp, ClusterScaleFailed, "Error", nil, nil},
    
    // 删除阶段
    {ClusterReady, ClusterDeleting, "EnsureDeleteOrReset", needDelete, nil},
    {ClusterDeleting, "", "Success", nil, nil},  // 空表示资源消失
    {ClusterDeleting, ClusterDeleteFailed, "Error", nil, nil},
    
    // ... 更多转换规则
}

// StateMachineEngine 状态机引擎
type StateMachineEngine struct {
    transitions []StateTransition
    validator   *StateValidator
    recorder    *EventRecorder
}

// Transition 执行状态转换
func (e *StateMachineEngine) Transition(cluster *BKECluster, trigger string, err error) error {
    currentState := cluster.Status.ClusterStatus
    
    // 查找匹配的转换规则
    for _, trans := range e.transitions {
        if trans.From == currentState && trans.Trigger == trigger {
            // 检查转换条件
            if trans.Condition != nil && !trans.Condition(cluster) {
                continue
            }
            
            // 执行转换动作
            if trans.Action != nil {
                if actErr := trans.Action(cluster); actErr != nil {
                    return actErr
                }
            }
            
            // 应用新状态
            cluster.Status.ClusterStatus = trans.To
            
            // 记录转换事件
            e.recorder.Record(StateTransitionEvent{
                From:      currentState,
                To:        trans.To,
                Trigger:   trigger,
                Success:   err == nil,
                Timestamp: time.Now(),
            })
            
            return nil
        }
    }
    
    return fmt.Errorf("no valid transition from %s with trigger %s", currentState, trigger)
}
```

#### 3.2.2 状态验证器

```go
// pkg/statemachine/validator.go

type StateValidator struct {
    rules []StateValidationRule
}

type StateValidationRule struct {
    From       ClusterStatus
    To         ClusterStatus
    Validators []func(*BKECluster) error
}

func (v *StateValidator) Validate(from, to ClusterStatus, cluster *BKECluster) error {
    for _, rule := range v.rules {
        if rule.From == from && rule.To == to {
            for _, validator := range rule.Validators {
                if err := validator(cluster); err != nil {
                    return fmt.Errorf("state validation failed: %v", err)
                }
            }
            return nil
        }
    }
    return nil // 未找到规则，允许转换（向后兼容）
}
```

### 3.3 CVO 集成

#### 3.3.1 ClusterVersionReconciler

```go
// controllers/capbke/clusterversion_controller.go

type ClusterVersionReconciler struct {
    client.Client
    Scheme        *runtime.Scheme
    DAGScheduler  *dagexec.Scheduler
    StateMachine  *statemachine.StateMachineEngine
    ManifestStore *manifest.Store
}

func (r *ClusterVersionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    cv := &cvoapi.ClusterVersion{}
    if err := r.Get(ctx, req.NamespacedName, cv); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 1. 检查版本是否变更
    if cv.Status.CurrentVersion == cv.Spec.DesiredVersion {
        return ctrl.Result{}, nil
    }
    
    // 2. 解析 ReleaseImage 获取组件清单
    ri, err := r.ManifestStore.GetReleaseImage(cv.Spec.DesiredVersion)
    if err != nil {
        return ctrl.Result{}, err
    }
    
    // 3. 构建升级 DAG
    dag := BuildUpgradeDAGFromReleaseImage(ri)
    
    // 4. 执行 DAG
    if err := r.DAGScheduler.ExecuteDAG(ctx, dag); err != nil {
        // 更新 ClusterVersion 状态为 Degraded
        cv.Status.Phase = "Degraded"
        cv.Status.FailureMessage = err.Error()
        r.Status().Update(ctx, cv)
        return ctrl.Result{}, err
    }
    
    // 5. 更新 ClusterVersion 状态
    cv.Status.CurrentVersion = cv.Spec.DesiredVersion
    cv.Status.Phase = "Available"
    r.Status().Update(ctx, cv)
    
    return ctrl.Result{}, nil
}
```

#### 3.3.2 BKEClusterReconciler 简化

```go
// controllers/capbke/bkecluster_controller.go (重构后)

func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. 获取 BKECluster
    bkeCluster := &bkev1beta1.BKECluster{}
    if err := r.Get(ctx, req.NamespacedName, bkeCluster); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 2. 确保 ClusterVersion 存在
    if err := r.ensureClusterVersion(ctx, bkeCluster); err != nil {
        return ctrl.Result{}, err
    }
    
    // 3. 构建并执行 DAG (根据场景选择安装或升级 DAG)
    dag := r.selectDAG(bkeCluster)
    if err := r.DAGScheduler.ExecuteDAG(ctx, dag); err != nil {
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}

func (r *BKEClusterReconciler) selectDAG(bkeCluster *bkev1beta1.BKECluster) *dagexec.DAG {
    if bkeCluster.Status.CurrentVersion == "" {
        return dagexec.BuildInstallDAG()
    }
    return dagexec.BuildUpgradeDAG()
}
```

### 3.4 StatusManager 重构

#### 3.4.1 改进的状态管理器

```go
// pkg/statusmanage/statusmanager_v2.go

type StatusManagerV2 struct {
    cmux sync.RWMutex
    nmux sync.RWMutex
    
    BKEClusterStatusMap map[string]*StatusRecordV2
    BKENodesStatusMap   map[string]map[string]*StatusRecordV2
    
    // 新增：状态清理器
    cleaner *StatusCleaner
}

type StatusRecordV2 struct {
    LatestFailedState   string
    LatestNormalState   string
    StatusCount         int32
    NeedRequeue         bool
    CurrentClusterState ClusterHealthState
    
    // 新增字段
    LastUpdateTime  time.Time
    ExpireTime      time.Time      // 过期时间
    PhaseName       string         // 关联的Phase
    RetryPolicy     RetryPolicy    // 重试策略
}

type RetryPolicy struct {
    MaxRetryCount   int
    BackoffStrategy BackoffType
    InitialDelay    time.Duration
    MaxDelay        time.Duration
}

type StatusCleaner struct {
    cleanupInterval time.Duration
    expireDuration  time.Duration
    stopCh          chan struct{}
}

func (c *StatusCleaner) Start() {
    ticker := time.NewTicker(c.cleanupInterval)
    go func() {
        for {
            select {
            case <-ticker.C:
                c.cleanupExpiredRecords()
            case <-c.stopCh:
                ticker.Stop()
                return
            }
        }
    }()
}

func (c *StatusCleaner) cleanupExpiredRecords() {
    now := time.Now()
    for key, record := range BKEClusterStatusMap {
        if now.After(record.ExpireTime) {
            delete(BKEClusterStatusMap, key)
        }
    }
}
```

## 四、迁移策略

### 4.1 分阶段迁移

| 阶段 | 时间 | 内容 | 风险 |
|------|------|------|------|
| **Phase 1** | 第1个月 | 引入状态转换表 + 状态机引擎 | 低 (与现有逻辑并存) |
| **Phase 2** | 第2个月 | 引入统一DAG调度器 | 中 (需要充分测试) |
| **Phase 3** | 第3个月 | 重构StatusManager | 低 (独立组件) |
| **Phase 4** | 第4个月 | 移除旧PhaseFlow | 高 (需要灰度发布) |

### 4.2 Feature Gate 控制

```go
// pkg/featuregate/features.go

const (
    StateMachineEngine   = "StateMachineEngine"    // 启用状态机引擎
    UnifiedDAGScheduler  = "UnifiedDAGScheduler"   // 启用统一DAG调度器
    StatusManagerV2      = "StatusManagerV2"       // 启用新版StatusManager
)

// 默认关闭，逐步启用
var defaultFeatureGates = map[string]bool{
    StateMachineEngine:   false,
    UnifiedDAGScheduler:  false,
    StatusManagerV2:      true,  // 先启用低风险组件
}
```

### 4.3 向后兼容

```go
// 兼容层：同时支持新旧两种方式
func (r *BKEClusterReconciler) executePhaseFlow(ctx context.Context, bkeCluster *BKECluster) error {
    if featuregate.Enabled(UnifiedDAGScheduler) {
        // 新方式：使用DAG调度器
        dag := r.selectDAG(bkeCluster)
        return r.DAGScheduler.ExecuteDAG(ctx, dag)
    }
    
    // 旧方式：使用PhaseFlow
    flow := phases.NewPhaseFlow(phaseCtx)
    flow.CalculatePhase(old, new)
    return flow.Execute()
}
```

## 五、收益评估

| 指标 | 当前 | 重构后 | 提升 |
|------|------|--------|------|
| **升级耗时** | ~45min | ~15min | ↓67% |
| **代码分支** | 11个switch-case | 1个状态转换表 | ↓90% |
| **新增Phase成本** | 修改3+处代码 | 添加DAG节点 | ↓80% |
| **并行度** | 0 (全串行) | 3-5层并行 | 显著提升 |
| **可观测性** | 无事件记录 | 完整事件系统 | 从0到1 |
| **内存泄漏风险** | 高 (无限增长) | 低 (自动清理) | 消除风险 |

## 六、风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| DAG构建错误 | 升级流程中断 | 充分单元测试 + 集成测试 |
| 状态转换表不完整 | 非法状态转换 | 状态验证器 + 默认回退 |
| 并行执行竞态 | 数据不一致 | errgroup + 锁保护 |
| 迁移期间双轨运行 | 行为不一致 | Feature Gate 控制 + 灰度发布 |

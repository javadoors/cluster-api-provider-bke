
# 阶段二的详细设计
## 阶段二：状态机引擎 — 替换 PhaseFlow 编排 详细设计
### 一、当前 PhaseFlow 架构问题总结
通过分析 [phase_flow.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go)、[base.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/base.go)、[context.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/context.go) 和 [list.go](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go)，核心问题如下：

| # | 问题 | 位置 | 影响 |
|---|------|------|------|
| 1 | **两阶段执行模型**：CalculatePhase 和 Execute 在两次 Reconcile 中执行 | [phase_flow.go:72-78](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L72) | 中间状态可被外部修改，导致状态不一致 |
| 2 | **NeedExecute 双重调用**：Calculate 和 Execute 各调一次 | [phase_flow.go:88](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L88) 和 [phase_flow.go:314](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L314) | 两次结果可能不一致 |
| 3 | **隐式依赖关系**：阶段顺序通过列表注册顺序定义 | [list.go:28-63](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/list.go#L28) | 无法表达并行、条件依赖 |
| 4 | **WatchBKEClusterStatus 协程泄漏**：`go p.ctx.WatchBKEClusterStatus()` 无生命周期管理 | [phase_flow.go:143](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L143) | panic 时协程可能无法退出 |
| 5 | **状态上报逻辑复杂**：4 个 handle*Status 方法逻辑相似但细微差别 | [base.go:259-367](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/base.go#L259) | 难以维护 |
| 6 | **集群状态与 Phase 状态耦合**：`calculateClusterStatusByPhase` 用 switch-case 映射 | [phase_flow.go:366-416](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L366) | 新增 Phase 需同步修改映射 |
| 7 | **频繁 API 调用**：每个 Phase 至少 3 次 API 调用（Refresh + RefreshCluster + Report） | [base.go:78-92](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/base.go#L78) | 20+ Phase = 60+ API 调用 |
### 二、状态机核心设计
#### 2.1 状态模型定义
当前系统的集群状态（`ClusterStatus`）和 Phase 状态（`PhaseStatus`）是分离的，状态机将它们统一为一个连贯的状态模型。

**集群生命周期状态（ClusterLifecycleState）**：
```
                    ┌──────────────┐
                    │   None       │ ← 初始状态
                    └──────┬───────┘
                           │ spec 创建
                           ▼
                   ┌──────────────┐
            ┌──────│  Provisioning│──────┐
            │      └──────┬───────┘      │
            │ fail        │ success      │ fail
            ▼             ▼              ▼
   ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
   │  InitFailed  │ │   Running    │ │  Failed      │
   └──────────────┘ └──────┬───────┘ └──────────────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
      ┌──────────────┐ ┌──────────┐ ┌──────────┐
      │  Upgrading   │ │ Scaling  │ │ Managing │
      └──────┬───────┘ └────┬─────┘ └────┬─────┘
             │              │            │
             ▼              ▼            ▼
      ┌──────────────┐ ┌───────────┐ ┌────────────┐
      │UpgradeFailed │ │ScaleFailed│ │ManageFailed│
      └──────────────┘ └───────────┘ └────────────┘
                           │
                    ┌──────┴───────┐
                    │  Deleting    │
                    └──────────────┘
```
**状态机接口定义** — 新文件 `pkg/statemachine/statemachine.go`：
```go
package statemachine

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeerrors "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/errors"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
)

type ClusterLifecycleState string

const (
	StateNone          ClusterLifecycleState = ""
	StateProvisioning  ClusterLifecycleState = "Provisioning"
	StateRunning       ClusterLifecycleState = "Running"
	StateUpgrading     ClusterLifecycleState = "Upgrading"
	StateScaling       ClusterLifecycleState = "Scaling"
	StateManaging      ClusterLifecycleState = "Managing"
	StateDeleting      ClusterLifecycleState = "Deleting"
	StatePausing       ClusterLifecycleState = "Pausing"
	StateDryRunning    ClusterLifecycleState = "DryRunning"
	StateInitFailed    ClusterLifecycleState = "InitFailed"
	StateUpgradeFailed ClusterLifecycleState = "UpgradeFailed"
	StateScaleFailed   ClusterLifecycleState = "ScaleFailed"
	StateManageFailed  ClusterLifecycleState = "ManageFailed"
	StateDeleteFailed  ClusterLifecycleState = "DeleteFailed"
	StatePauseFailed   ClusterLifecycleState = "PauseFailed"
	StateDryRunFailed  ClusterLifecycleState = "DryRunFailed"
)

type StateTransition struct {
	From  ClusterLifecycleState
	To    ClusterLifecycleState
	Event string
}

type State interface {
	Name() ClusterLifecycleState
	Enter(ctx *StateContext) error
	Execute(ctx *StateContext) (ctrl.Result, *StateTransition, error)
	Exit(ctx *StateContext) error
	CanTransitionTo(target ClusterLifecycleState) bool
}

type StateContext struct {
	context.Context
	Client       client.Client
	BKECluster   *bkev1beta1.BKECluster
	OldBKECluster *bkev1beta1.BKECluster
	Log          *bkev1beta1.BKELogger
	Scheme       *runtime.Scheme
	RestConfig   *rest.Config
	NodeFetcher  *nodeutil.NodeFetcher
}

type ClusterStateMachine struct {
	states       map[ClusterLifecycleState]State
	currentState ClusterLifecycleState
	transitions  []StateTransition
}

func NewClusterStateMachine() *ClusterStateMachine {
	sm := &ClusterStateMachine{
		states:      make(map[ClusterLifecycleState]State),
		transitions: defaultTransitions(),
	}
	sm.registerStates()
	return sm
}

func defaultTransitions() []StateTransition {
	return []StateTransition{
		{From: StateNone, To: StateProvisioning, Event: "provision"},
		{From: StateNone, To: StateDeleting, Event: "delete"},
		{From: StateNone, To: StateManaging, Event: "manage"},
		{From: StateNone, To: StateDryRunning, Event: "dryrun"},
		{From: StateNone, To: StatePausing, Event: "pause"},
		{From: StateProvisioning, To: StateRunning, Event: "provision_success"},
		{From: StateProvisioning, To: StateInitFailed, Event: "provision_fail"},
		{From: StateRunning, To: StateUpgrading, Event: "upgrade"},
		{From: StateRunning, To: StateScaling, Event: "scale"},
		{From: StateRunning, To: StateDeleting, Event: "delete"},
		{From: StateRunning, To: StatePausing, Event: "pause"},
		{From: StateRunning, To: StateDryRunning, Event: "dryrun"},
		{From: StateUpgrading, To: StateRunning, Event: "upgrade_success"},
		{From: StateUpgrading, To: StateUpgradeFailed, Event: "upgrade_fail"},
		{From: StateScaling, To: StateRunning, Event: "scale_success"},
		{From: StateScaling, To: StateScaleFailed, Event: "scale_fail"},
		{From: StateManaging, To: StateRunning, Event: "manage_success"},
		{From: StateManaging, To: StateManageFailed, Event: "manage_fail"},
		{From: StateDeleting, To: StateNone, Event: "delete_success"},
		{From: StateDeleting, To: StateDeleteFailed, Event: "delete_fail"},
		{From: StatePausing, To: StateRunning, Event: "pause_success"},
		{From: StatePausing, To: StatePauseFailed, Event: "pause_fail"},
		{From: StateDryRunning, To: StateRunning, Event: "dryrun_success"},
		{From: StateDryRunning, To: StateDryRunFailed, Event: "dryrun_fail"},
		{From: StateInitFailed, To: StateProvisioning, Event: "retry"},
		{From: StateUpgradeFailed, To: StateUpgrading, Event: "retry"},
		{From: StateScaleFailed, To: StateScaling, Event: "retry"},
		{From: StateManageFailed, To: StateManaging, Event: "retry"},
		{From: StateDeleteFailed, To: StateDeleting, Event: "retry"},
		{From: StatePauseFailed, To: StatePausing, Event: "retry"},
		{From: StateDryRunFailed, To: StateDryRunning, Event: "retry"},
	}
}

func (sm *ClusterStateMachine) registerStates() {
	sm.states[StateProvisioning] = NewProvisioningState()
	sm.states[StateRunning] = NewRunningState()
	sm.states[StateUpgrading] = NewUpgradingState()
	sm.states[StateScaling] = NewScalingState()
	sm.states[StateManaging] = NewManagingState()
	sm.states[StateDeleting] = NewDeletingState()
	sm.states[StatePausing] = NewPausingState()
	sm.states[StateDryRunning] = NewDryRunningState()
	sm.states[StateInitFailed] = NewFailedState(StateInitFailed, StateProvisioning)
	sm.states[StateUpgradeFailed] = NewFailedState(StateUpgradeFailed, StateUpgrading)
	sm.states[StateScaleFailed] = NewFailedState(StateScaleFailed, StateScaling)
	sm.states[StateManageFailed] = NewFailedState(StateManageFailed, StateManaging)
	sm.states[StateDeleteFailed] = NewFailedState(StateDeleteFailed, StateDeleting)
	sm.states[StatePauseFailed] = NewFailedState(StatePauseFailed, StatePausing)
	sm.states[StateDryRunFailed] = NewFailedState(StateDryRunFailed, StateDryRunning)
}

func (sm *ClusterStateMachine) Reconcile(ctx *StateContext) (ctrl.Result, error) {
	currentState := sm.determineCurrentState(ctx.BKECluster)
	sm.currentState = currentState

	state, ok := sm.states[currentState]
	if !ok {
		return ctrl.Result{}, bkeerrors.NewPermanent("unknown state",
			bkeerrors.WithReason("InvalidState"))
	}

	if err := state.Enter(ctx); err != nil {
		return ctrl.Result{}, bkeerrors.WrapTransient(err, "enter state failed")
	}

	result, transition, err := state.Execute(ctx)
	if err != nil {
		return sm.handleExecutionError(ctx, err)
	}

	if transition != nil {
		if err := sm.transition(ctx, state, transition); err != nil {
			return ctrl.Result{}, err
		}
	}

	return result, nil
}

func (sm *ClusterStateMachine) determineCurrentState(cluster *bkev1beta1.BKECluster) ClusterLifecycleState {
	if !cluster.DeletionTimestamp.IsZero() {
		return StateDeleting
	}
	if cluster.Spec.Reset {
		return StateDeleting
	}
	if cluster.Spec.Pause {
		return StatePausing
	}
	if cluster.Spec.DryRun {
		return StateDryRunning
	}

	switch cluster.Status.ClusterStatus {
	case bkev1beta1.ClusterInitializing:
		return StateProvisioning
	case bkev1beta1.ClusterReady:
		return StateRunning
	case bkev1beta1.ClusterUpgrading:
		return StateUpgrading
	case bkev1beta1.ClusterMasterScalingUp, bkev1beta1.ClusterWorkerScalingUp,
		bkev1beta1.ClusterMasterScalingDown, bkev1beta1.ClusterWorkerScalingDown:
		return StateScaling
	case bkev1beta1.ClusterManaging:
		return StateManaging
	case bkev1beta1.ClusterDeleting:
		return StateDeleting
	case bkev1beta1.ClusterPaused:
		return StatePausing
	case bkev1beta1.ClusterDryRun:
		return StateDryRunning
	case bkev1beta1.ClusterInitializationFailed:
		return StateInitFailed
	case bkev1beta1.ClusterUpgradeFailed:
		return StateUpgradeFailed
	case bkev1beta1.ClusterScaleFailed:
		return StateScaleFailed
	case bkev1beta1.ClusterManageFailed:
		return StateManageFailed
	case bkev1beta1.ClusterDeleteFailed:
		return StateDeleteFailed
	case bkev1beta1.ClusterPauseFailed:
		return StatePauseFailed
	case bkev1beta1.ClusterDryRunFailed:
		return StateDryRunFailed
	default:
		if cluster.Status.Ready {
			return StateRunning
		}
		return StateProvisioning
	}
}

func (sm *ClusterStateMachine) transition(ctx *StateContext, fromState State, t *StateTransition) error {
	targetState, ok := sm.states[t.To]
	if !ok {
		return bkeerrors.NewPermanent("unknown target state",
			bkeerrors.WithReason("InvalidTransition"))
	}

	if !fromState.CanTransitionTo(t.To) {
		return bkeerrors.NewPermanent("invalid transition",
			bkeerrors.WithReason("TransitionNotAllowed"),
			bkeerrors.WithPhaseName(string(fromState.Name())))
	}

	if err := fromState.Exit(ctx); err != nil {
		return bkeerrors.WrapTransient(err, "exit state failed")
	}

	if err := targetState.Enter(ctx); err != nil {
		return bkeerrors.WrapTransient(err, "enter state failed")
	}

	sm.currentState = t.To
	ctx.BKECluster.Status.ClusterStatus = confv1beta1.ClusterStatus(t.To)
	return nil
}

func (sm *ClusterStateMachine) handleExecutionError(ctx *StateContext, err error) (ctrl.Result, error) {
	if bkeerrors.IsPermanentError(err) {
		return ctrl.Result{}, nil
	}
	retryAfter := bkeerrors.GetRetryAfter(err)
	if retryAfter > 0 {
		return ctrl.Result{RequeueAfter: retryAfter}, nil
	}
	return ctrl.Result{}, err
}
```
#### 2.2 State 接口与基础实现
新文件 `pkg/statemachine/state.go`：
```go
package statemachine

type BaseState struct {
	name           ClusterLifecycleState
	enterHooks     []func(ctx *StateContext) error
	exitHooks      []func(ctx *StateContext) error
	allowedTargets []ClusterLifecycleState
}

func NewBaseState(name ClusterLifecycleState, allowedTargets ...ClusterLifecycleState) BaseState {
	return BaseState{
		name:           name,
		allowedTargets: allowedTargets,
	}
}

func (s *BaseState) Name() ClusterLifecycleState {
	return s.name
}

func (s *BaseState) Enter(ctx *StateContext) error {
	for _, hook := range s.enterHooks {
		if err := hook(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *BaseState) Exit(ctx *StateContext) error {
	for _, hook := range s.exitHooks {
		if err := hook(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *BaseState) CanTransitionTo(target ClusterLifecycleState) bool {
	for _, allowed := range s.allowedTargets {
		if allowed == target {
			return true
		}
	}
	return false
}

func (s *BaseState) RegisterEnterHooks(hooks ...func(ctx *StateContext) error) {
	s.enterHooks = append(s.enterHooks, hooks...)
}

func (s *BaseState) RegisterExitHooks(hooks ...func(ctx *StateContext) error) {
	s.exitHooks = append(s.exitHooks, hooks...)
}
```
#### 2.3 PhaseAdapter — 适配现有 Phase 实现
**核心设计思想**：状态机不重写每个 Phase 的业务逻辑，而是通过 `PhaseAdapter` 将现有 Phase 适配到 State 的 Execute 方法中。

新文件 `pkg/statemachine/phase_adapter.go`：
```go
package statemachine

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeerrors "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/errors"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phases"
)

type PhaseStep struct {
	Name       confv1beta1.BKEClusterPhase
	Factory    func(ctx *phaseframe.PhaseContext) phaseframe.Phase
	DependsOn  []confv1beta1.BKEClusterPhase
	Timeout    time.Duration
	Retryable  bool
}

type PhaseAdapter struct {
	BaseState
	steps       []PhaseStep
	phaseCtx    *phaseframe.PhaseContext
	successEvent string
	failEvent    string
}

func NewPhaseAdapter(
	name ClusterLifecycleState,
	steps []PhaseStep,
	successEvent, failEvent string,
	allowedTargets ...ClusterLifecycleState,
) *PhaseAdapter {
	return &PhaseAdapter{
		BaseState:    NewBaseState(name, allowedTargets...),
		steps:        steps,
		successEvent: successEvent,
		failEvent:    failEvent,
	}
}

func (a *PhaseAdapter) Execute(ctx *StateContext) (ctrl.Result, *StateTransition, error) {
	a.phaseCtx = a.buildPhaseContext(ctx)

	var res ctrl.Result
	var lastErr error

	for _, step := range a.steps {
		phase := step.Factory(a.phaseCtx)

		if !phase.NeedExecute(ctx.OldBKECluster, ctx.BKECluster) {
			phase.SetStatus(bkev1beta1.PhaseSkipped)
			if err := phase.Report("", false); err != nil {
				return ctrl.Result{}, nil, bkeerrors.WrapTransient(err, "report skipped status failed")
			}
			continue
		}

		if err := phase.ExecutePreHook(); err != nil {
			return ctrl.Result{}, nil, bkeerrors.WrapTransient(err, "pre hook failed",
				bkeerrors.WithPhaseName(string(step.Name)))
		}

		phaseResult, phaseErr := phase.Execute()
		if phaseErr != nil {
			lastErr = phaseErr
			_ = phase.ExecutePostHook(phaseErr)
			return a.handleStepError(step, phaseErr)
		}

		res = util.LowestNonZeroResult(res, phaseResult)

		if err := phase.ExecutePostHook(nil); err != nil {
			return ctrl.Result{}, nil, bkeerrors.WrapTransient(err, "post hook failed",
				bkeerrors.WithPhaseName(string(step.Name)))
		}

		if err := a.refreshCluster(ctx); err != nil {
			return ctrl.Result{}, nil, bkeerrors.WrapTransient(err, "refresh cluster failed")
		}
	}

	return res, &StateTransition{
		From:  a.name,
		To:    a.targetFromEvent(a.successEvent),
		Event: a.successEvent,
	}, nil
}

func (a *PhaseAdapter) handleStepError(step PhaseStep, err error) (ctrl.Result, *StateTransition, error) {
	if step.Retryable {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil, bkeerrors.WrapTransient(err,
			fmt.Sprintf("step %s failed, will retry", step.Name),
			bkeerrors.WithPhaseName(string(step.Name)))
	}

	return ctrl.Result{}, &StateTransition{
		From:  a.name,
		To:    a.targetFromEvent(a.failEvent),
		Event: a.failEvent,
	}, bkeerrors.WrapPermanent(err, fmt.Sprintf("step %s failed permanently", step.Name),
		bkeerrors.WithPhaseName(string(step.Name)))
}

func (a *PhaseAdapter) buildPhaseContext(stateCtx *StateContext) *phaseframe.PhaseContext {
	return phaseframe.NewReconcilePhaseCtx(stateCtx.Context).
		SetClient(stateCtx.Client).
		SetRestConfig(stateCtx.RestConfig).
		SetScheme(stateCtx.Scheme).
		SetLogger(stateCtx.Log).
		SetBKECluster(stateCtx.BKECluster)
}

func (a *PhaseAdapter) refreshCluster(ctx *StateContext) error {
	newCluster, err := mergecluster.GetCombinedBKECluster(ctx, ctx.Client,
		ctx.BKECluster.Namespace, ctx.BKECluster.Name)
	if err != nil {
		return err
	}
	ctx.BKECluster = newCluster
	a.phaseCtx.SetBKECluster(newCluster)
	return nil
}

func (a *PhaseAdapter) targetFromEvent(event string) ClusterLifecycleState {
	for _, t := range defaultTransitions() {
		if t.From == a.name && t.Event == event {
			return t.To
		}
	}
	return a.name
}
```
#### 2.4 各 State 实现 — 使用 PhaseAdapter 组装
新文件 `pkg/statemachine/states.go`：
```go
package statemachine

func NewProvisioningState() State {
	steps := []PhaseStep{
		{Name: phases.EnsureFinalizerName, Factory: phases.NewEnsureFinalizer, Retryable: false},
		{Name: phases.EnsureBKEAgentName, Factory: phases.NewEnsureBKEAgent, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureFinalizerName}, Retryable: true},
		{Name: phases.EnsureNodesEnvName, Factory: phases.NewEnsureNodesEnv, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureBKEAgentName}, Retryable: true},
		{Name: phases.EnsureClusterAPIObjName, Factory: phases.NewEnsureClusterAPIObj, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureNodesEnvName}, Retryable: true},
		{Name: phases.EnsureCertsName, Factory: phases.NewEnsureCerts, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureClusterAPIObjName}, Retryable: false},
		{Name: phases.EnsureLoadBalanceName, Factory: phases.NewEnsureLoadBalance, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureCertsName}, Retryable: true},
		{Name: phases.EnsureMasterInitName, Factory: phases.NewEnsureMasterInit, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureLoadBalanceName}, Retryable: true},
		{Name: phases.EnsureMasterJoinName, Factory: phases.NewEnsureMasterJoin, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureMasterInitName}, Retryable: true},
		{Name: phases.EnsureWorkerJoinName, Factory: phases.NewEnsureWorkerJoin, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureMasterJoinName}, Retryable: true},
		{Name: phases.EnsureAddonDeployName, Factory: phases.NewEnsureAddonDeploy, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureWorkerJoinName}, Retryable: true},
		{Name: phases.EnsureNodesPostProcessName, Factory: phases.NewEnsureNodesPostProcess, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureAddonDeployName}, Retryable: true},
		{Name: phases.EnsureAgentSwitchName, Factory: phases.NewEnsureAgentSwitch, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureNodesPostProcessName}, Retryable: true},
	}

	return NewPhaseAdapter(
		StateProvisioning,
		steps,
		"provision_success",
		"provision_fail",
		StateRunning, StateInitFailed,
	)
}

func NewUpgradingState() State {
	steps := []PhaseStep{
		{Name: phases.EnsureProviderSelfUpgradeName, Factory: phases.NewEnsureProviderSelfUpgrade, Retryable: false},
		{Name: phases.EnsureAgentUpgradeName, Factory: phases.NewEnsureAgentUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureProviderSelfUpgradeName}, Retryable: true},
		{Name: phases.EnsureContainerdUpgradeName, Factory: phases.NewEnsureContainerdUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureAgentUpgradeName}, Retryable: true},
		{Name: phases.EnsureEtcdUpgradeName, Factory: phases.NewEnsureEtcdUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureContainerdUpgradeName}, Retryable: true},
		{Name: phases.EnsureMasterUpgradeName, Factory: phases.NewEnsureMasterUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureEtcdUpgradeName}, Retryable: true},
		{Name: phases.EnsureWorkerUpgradeName, Factory: phases.NewEnsureWorkerUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureMasterUpgradeName}, Retryable: true},
		{Name: phases.EnsureComponentUpgradeName, Factory: phases.NewEnsureComponentUpgrade, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsureWorkerUpgradeName}, Retryable: true},
	}

	return NewPhaseAdapter(
		StateUpgrading,
		steps,
		"upgrade_success",
		"upgrade_fail",
		StateRunning, StateUpgradeFailed,
	)
}

func NewScalingState() State {
	return NewScalingStateWithDetector()
}

func NewDeletingState() State {
	steps := []PhaseStep{
		{Name: phases.EnsurePausedName, Factory: phases.NewEnsurePaused, Retryable: true},
		{Name: phases.EnsureDeleteOrResetName, Factory: phases.NewEnsureDeleteOrReset, DependsOn: []confv1beta1.BKEClusterPhase{phases.EnsurePausedName}, Retryable: true},
	}

	return NewPhaseAdapter(
		StateDeleting,
		steps,
		"delete_success",
		"delete_fail",
		StateNone, StateDeleteFailed,
	)
}

func NewPausingState() State {
	steps := []PhaseStep{
		{Name: phases.EnsurePausedName, Factory: phases.NewEnsurePaused, Retryable: true},
	}

	return NewPhaseAdapter(
		StatePausing,
		steps,
		"pause_success",
		"pause_fail",
		StateRunning, StatePauseFailed,
	)
}

func NewDryRunningState() State {
	steps := []PhaseStep{
		{Name: phases.EnsureDryRunName, Factory: phases.NewEnsureDryRun, Retryable: false},
	}

	return NewPhaseAdapter(
		StateDryRunning,
		steps,
		"dryrun_success",
		"dryrun_fail",
		StateRunning, StateDryRunFailed,
	)
}

func NewManagingState() State {
	steps := []PhaseStep{
		{Name: phases.EnsureClusterManageName, Factory: phases.NewEnsureClusterManage, Retryable: true},
	}

	return NewPhaseAdapter(
		StateManaging,
		steps,
		"manage_success",
		"manage_fail",
		StateRunning, StateManageFailed,
	)
}

func NewRunningState() State {
	return &RunningState{
		BaseState: NewBaseState(StateRunning,
			StateUpgrading, StateScaling, StateDeleting,
			StatePausing, StateDryRunning, StateManaging),
	}
}

type RunningState struct {
	BaseState
}

func (s *RunningState) Execute(ctx *StateContext) (ctrl.Result, *StateTransition, error) {
	transition := s.detectTransition(ctx)
	if transition != nil {
		return ctrl.Result{}, transition, nil
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil, nil
}

func (s *RunningState) detectTransition(ctx *StateContext) *StateTransition {
	cluster := ctx.BKECluster

	if !cluster.DeletionTimestamp.IsZero() {
		return &StateTransition{From: StateRunning, To: StateDeleting, Event: "delete"}
	}
	if cluster.Spec.Reset {
		return &StateTransition{From: StateRunning, To: StateDeleting, Event: "delete"}
	}
	if cluster.Spec.Pause {
		return &StateTransition{From: StateRunning, To: StatePausing, Event: "pause"}
	}
	if cluster.Spec.DryRun {
		return &StateTransition{From: StateRunning, To: StateDryRunning, Event: "dryrun"}
	}

	old := ctx.OldBKECluster
	if old != nil {
		if s.isUpgradeRequested(old, cluster) {
			return &StateTransition{From: StateRunning, To: StateUpgrading, Event: "upgrade"}
		}
		if s.isScaleRequested(old, cluster) {
			return &StateTransition{From: StateRunning, To: StateScaling, Event: "scale"}
		}
	}

	return nil
}

func (s *RunningState) isUpgradeRequested(old, new *bkev1beta1.BKECluster) bool {
	if new.Spec.ClusterConfig == nil || old.Spec.ClusterConfig == nil {
		return false
	}
	return new.Spec.ClusterConfig.Cluster.KubernetesVersion != old.Spec.ClusterConfig.Cluster.KubernetesVersion ||
		new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion != old.Spec.ClusterConfig.Cluster.OpenFuyaoVersion ||
		new.Spec.ClusterConfig.Cluster.ContainerdVersion != old.Spec.ClusterConfig.Cluster.ContainerdVersion
}

func (s *RunningState) isScaleRequested(old, new *bkev1beta1.BKECluster) bool {
	return false
}

func NewFailedState(name, retryTarget ClusterLifecycleState) State {
	return &FailedState{
		BaseState:    NewBaseState(name, retryTarget),
		retryTarget:  retryTarget,
	}
}

type FailedState struct {
	BaseState
	retryTarget ClusterLifecycleState
}

func (s *FailedState) Execute(ctx *StateContext) (ctrl.Result, *StateTransition, error) {
	return ctrl.Result{}, &StateTransition{
		From:  s.name,
		To:    s.retryTarget,
		Event: "retry",
	}, nil
}
```
#### 2.5 ScalingState — 动态步骤组装
ScalingState 比较特殊，需要根据扩缩容方向动态组装步骤：
```go
package statemachine

import (
	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phases"
)

func NewScalingStateWithDetector() State {
	return &ScalingState{
		BaseState: NewBaseState(StateScaling, StateRunning, StateScaleFailed),
	}
}

type ScalingState struct {
	BaseState
}

func (s *ScalingState) Execute(ctx *StateContext) (ctrl.Result, *StateTransition, error) {
	direction := s.detectScaleDirection(ctx)
	if direction == ScaleDirectionNone {
		return ctrl.Result{}, &StateTransition{
			From: StateScaling, To: StateRunning, Event: "scale_success",
		}, nil
	}

	steps := s.buildSteps(direction)
	adapter := NewPhaseAdapter(
		StateScaling, steps,
		"scale_success", "scale_fail",
		StateRunning, StateScaleFailed,
	)
	return adapter.Execute(ctx)
}

type ScaleDirection string

const (
	ScaleDirectionNone        ScaleDirection = "None"
	ScaleDirectionMasterUp    ScaleDirection = "MasterUp"
	ScaleDirectionMasterDown  ScaleDirection = "MasterDown"
	ScaleDirectionWorkerUp    ScaleDirection = "WorkerUp"
	ScaleDirectionWorkerDown  ScaleDirection = "WorkerDown"
)

func (s *ScalingState) detectScaleDirection(ctx *StateContext) ScaleDirection {
	old := ctx.OldBKECluster
	new := ctx.BKECluster
	if old == nil || new == nil {
		return ScaleDirectionNone
	}

	switch ctx.BKECluster.Status.ClusterStatus {
	case bkev1beta1.ClusterMasterScalingUp:
		return ScaleDirectionMasterUp
	case bkev1beta1.ClusterMasterScalingDown:
		return ScaleDirectionMasterDown
	case bkev1beta1.ClusterWorkerScalingUp:
		return ScaleDirectionWorkerUp
	case bkev1beta1.ClusterWorkerScalingDown:
		return ScaleDirectionWorkerDown
	default:
		return ScaleDirectionNone
	}
}

func (s *ScalingState) buildSteps(direction ScaleDirection) []PhaseStep {
	switch direction {
	case ScaleDirectionMasterUp:
		return []PhaseStep{
			{Name: phases.EnsureMasterJoinName, Factory: phases.NewEnsureMasterJoin, Retryable: true},
		}
	case ScaleDirectionMasterDown:
		return []PhaseStep{
			{Name: phases.EnsureMasterDeleteName, Factory: phases.NewEnsureMasterDelete, Retryable: true},
		}
	case ScaleDirectionWorkerUp:
		return []PhaseStep{
			{Name: phases.EnsureWorkerJoinName, Factory: phases.NewEnsureWorkerJoin, Retryable: true},
		}
	case ScaleDirectionWorkerDown:
		return []PhaseStep{
			{Name: phases.EnsureWorkerDeleteName, Factory: phases.NewEnsureWorkerDelete, Retryable: true},
		}
	default:
		return nil
	}
}
```
### 三、Controller 层集成
修改 [bkecluster_controller.go](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go) 中的 `executePhaseFlow` 方法：

**当前代码**（[bkecluster_controller.go:144-162](file:///d:/code/github/cluster-api-provider-bke/controllers/capbke/bkecluster_controller.go#L144)）：
```go
func (r *BKEClusterReconciler) executePhaseFlow(ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    oldBkeCluster *bkev1beta1.BKECluster,
    bkeLogger *bkev1beta1.BKELogger) (ctrl.Result, error) {
    phaseCtx := phaseframe.NewReconcilePhaseCtx(ctx).
        SetClient(r.Client).
        SetRestConfig(r.RestConfig).
        SetScheme(r.Scheme).
        SetLogger(bkeLogger).
        SetBKECluster(bkeCluster)
    defer phaseCtx.Cancel()

    flow := phases.NewPhaseFlow(phaseCtx)
    err := flow.CalculatePhase(oldBkeCluster, bkeCluster)
    if err != nil {
        return ctrl.Result{}, err
    }
    res, err := flow.Execute()
    if err != nil {
        log.Warnf("Reconcile bkeCluster %q failed: %v", utils.ClientObjNS(bkeCluster), err)
    }
    return res, nil
}
```
**重构后**：
```go
func (r *BKEClusterReconciler) executePhaseFlow(ctx context.Context,
    bkeCluster *bkev1beta1.BKECluster,
    oldBkeCluster *bkev1beta1.BKECluster,
    bkeLogger *bkev1beta1.BKELogger) (ctrl.Result, error) {

    stateCtx := &statemachine.StateContext{
        Context:       ctx,
        Client:        r.Client,
        BKECluster:    bkeCluster,
        OldBKECluster: oldBkeCluster,
        Log:           bkeLogger,
        Scheme:        r.Scheme,
        RestConfig:    r.RestConfig,
        NodeFetcher:   r.NodeFetcher,
    }

    sm := statemachine.NewClusterStateMachine()
    result, err := sm.Reconcile(stateCtx)
    if err != nil {
        log.Warnf("Reconcile bkeCluster %q failed: %v", utils.ClientObjNS(bkeCluster), err)
    }
    return result, err
}
```
### 四、PhaseStatus 上报机制保留
状态机替换的是编排逻辑（PhaseFlow），但 PhaseStatus 上报机制保持不变。每个 Phase 的 `Report` 方法仍然将状态写入 `BKECluster.Status.PhaseStatus`，确保 UI 层可以继续展示进度。

状态机额外负责将 `ClusterStatus`（集群级别状态）与 `ClusterLifecycleState` 同步：
```go
func (sm *ClusterStateMachine) syncClusterStatus(ctx *StateContext) {
	ctx.BKECluster.Status.ClusterStatus = confv1beta1.ClusterStatus(sm.currentState)

	switch sm.currentState {
	case StateProvisioning:
		ctx.BKECluster.Status.ClusterHealthState = confv1beta1.Deploying
	case StateRunning:
		ctx.BKECluster.Status.ClusterHealthState = confv1beta1.Healthy
	case StateInitFailed:
		ctx.BKECluster.Status.ClusterHealthState = confv1beta1.DeployFailed
	case StateUpgrading:
		ctx.BKECluster.Status.ClusterHealthState = confv1beta1.Upgrading
	case StateUpgradeFailed:
		ctx.BKECluster.Status.ClusterHealthState = confv1beta1.UpgradeFailed
	}
}
```
这替代了当前 [phase_flow.go:366-416](file:///d:/code/github/cluster-api-provider-bke/pkg/phaseframe/phases/phase_flow.go#L366) 中 `calculateClusterStatusByPhase` 的 switch-case 映射。
### 五、消除 WatchBKEClusterStatus 协程
当前 `go p.ctx.WatchBKEClusterStatus()` 启动的协程用于：
1. 定期刷新 BKECluster 状态
2. 检测暂停状态
3. 检测删除状态并取消上下文

**重构方案**：将这些职责移入状态机的 `Execute` 方法中，通过 Reconcile 循环自然实现：
```go
func (sm *ClusterStateMachine) Reconcile(ctx *StateContext) (ctrl.Result, error) {
	if sm.shouldCancelExecution(ctx) {
		return ctrl.Result{}, nil
	}

	currentState := sm.determineCurrentState(ctx.BKECluster)
	sm.currentState = currentState

	state, ok := sm.states[currentState]
	if !ok {
		return ctrl.Result{}, bkeerrors.NewPermanent("unknown state")
	}

	if err := state.Enter(ctx); err != nil {
		return ctrl.Result{}, bkeerrors.WrapTransient(err, "enter state failed")
	}

	result, transition, err := state.Execute(ctx)
	if err != nil {
		return sm.handleExecutionError(ctx, err)
	}

	if transition != nil {
		if err := sm.transition(ctx, state, transition); err != nil {
			return ctrl.Result{}, err
		}
	}

	sm.syncClusterStatus(ctx)

	return result, nil
}

func (sm *ClusterStateMachine) shouldCancelExecution(ctx *StateContext) bool {
	cluster := ctx.BKECluster
	if !cluster.DeletionTimestamp.IsZero() && sm.currentState != StateDeleting {
		return true
	}
	return false
}
```
### 六、目录结构
```
pkg/statemachine/
├── statemachine.go        # ClusterStateMachine 核心逻辑
├── state.go               # BaseState 基础实现
├── states.go              # 各 State 实现（Provisioning/Upgrading/...）
├── phase_adapter.go       # PhaseAdapter 适配器
├── scaling_state.go       # ScalingState 动态步骤组装
├── transitions.go         # 状态转换表定义
├── statemachine_test.go   # 状态机核心测试
├── state_test.go          # BaseState 测试
├── phase_adapter_test.go  # PhaseAdapter 测试
└── transitions_test.go    # 转换规则测试
```
### 七、实现步骤
| 步骤 | 内容 | 涉及文件 | 风险 |
|------|------|---------|------|
| 1 | 创建 `pkg/statemachine/` 目录和核心接口 | 新文件 | 无 |
| 2 | 实现 `BaseState` 和 `ClusterStateMachine` | 新文件 | 无 |
| 3 | 实现 `PhaseAdapter` 适配器 | 新文件 | 无 |
| 4 | 实现各 State（Provisioning/Upgrading/...） | 新文件 | 低 — 复用现有 Phase |
| 5 | 实现状态转换表和 `determineCurrentState` | 新文件 | 中 — 需覆盖所有 ClusterStatus |
| 6 | 修改 Controller 的 `executePhaseFlow` | `bkecluster_controller.go` | 中 — 核心入口变更 |
| 7 | 移除 `WatchBKEClusterStatus` 协程依赖 | `context.go` | 低 — 职责已转移 |
| 8 | 编写单元测试 | 新文件 | 无 |
| 9 | 集成测试验证所有场景 | 全项目 | 需全部通过 |
### 八、验证标准
1. **功能等价**：所有现有 Phase 的 Execute/NeedExecute/Report 行为不变
2. **状态转换合法**：所有状态转换必须通过转换表验证
3. **单次 Reconcile**：消除两阶段执行模型，Calculate 和 Execute 合并
4. **无协程泄漏**：不再使用 `go WatchBKEClusterStatus()`
5. **错误分类**：Phase 执行错误通过阶段一的 `ReconcileError` 分类处理
6. **PhaseStatus 兼容**：UI 层仍可通过 `BKECluster.Status.PhaseStatus` 查看进度
7. **ClusterStatus 映射**：`ClusterLifecycleState` 与现有 `ClusterStatus` 双向映射正确
### 九、与阶段一的依赖关系
| 阶段一产出 | 阶段二使用方式 |
|-----------|--------------|
| `ReconcileError` 类型系统 | `handleExecutionError` 中分类处理错误 |
| `ReconcileErrorType` | `PhaseAdapter.handleStepError` 中标记 Retryable |
| `handleReconcileError` | Controller 层统一错误处理入口 |
| `InfrastructureMode` | `ProvisioningState` 中根据 IPI/UPI 跳过不同步骤 |
| `UpgradeStatus` | `UpgradingState` 中跟踪升级进度 |

# 集群生命周期状态说明     
## 集群生命周期状态详细说明
### 一、状态定义与语义
#### 1.1 状态分类
```
┌────────────────────────────────────────────────────────────────────────┐
│                        ClusterLifecycleState 分类                      │
├────────────────────────────────────────────────────────────────────────┤
│                                                                        │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ 初始状态                                                        │   │
│  │  • StateNone — 集群资源刚创建，尚未开始任何操作                 │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                        │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ 运行中状态— 集群正在进行某种操作                                │   │
│  │  • StateProvisioning  — 集群初始化部署                          │   │
│  │  • StateRunning       — 集群正常运行，无待处理操作              │   │
│  │  • StateUpgrading     — 集群版本升级中                          │   │
│  │  • StateScaling       — 集群节点扩缩容中                        │   │
│  │  • StateManaging      — 纳管现有集群中                          │   │
│  │  • StateDeleting      — 集群删除中                              │   │
│  │  • StatePausing       — 集群暂停中                              │   │
│  │  • StateDryRunning    — DryRun 模式中                           │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                        │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ 失败状态— 操作失败，等待重试或人工介入                          │   │
│  │  • StateInitFailed    — 初始化部署失败                          │   │
│  │  • StateUpgradeFailed — 版本升级失败                            │   │
│  │  • StateScaleFailed   — 节点扩缩容失败                          │   │
│  │  • StateManageFailed  — 纳管失败                                │   │
│  │  • StateDeleteFailed  — 删除失败                                │   │
│  │  • StatePauseFailed   — 暂停失败                                │   │
│  │  • StateDryRunFailed  — DryRun 失败                             │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                        │
└────────────────────────────────────────────────────────────────────────┘
```
#### 1.2 各状态详细说明
| 状态 | 中文名 | 触发条件 | 执行的 Phase | 结束状态 |
|------|--------|---------|-------------|---------|
| **StateNone** | 初始 | BKECluster CR 刚创建，Status 为空 | 无 | → Provisioning/Deleting/Managing/DryRunning/Pausing |
| **StateProvisioning** | 初始化部署 | 新集群首次部署 | EnsureFinalizer → EnsureBKEAgent → EnsureNodesEnv → EnsureClusterAPIObj → EnsureCerts → EnsureLoadBalance → EnsureMasterInit → EnsureMasterJoin → EnsureWorkerJoin → EnsureAddonDeploy → EnsureNodesPostProcess → EnsureAgentSwitch | → Running (成功) / InitFailed (失败) |
| **StateRunning** | 正常运行 | 集群部署完成，或升级/扩缩容完成 | EnsureCluster（健康检查） | → Upgrading/Scaling/Deleting/Pausing/DryRunning/Managing |
| **StateUpgrading** | 版本升级 | K8s/OpenFuyao/Containerd 版本变化 | EnsureProviderSelfUpgrade → EnsureAgentUpgrade → EnsureContainerdUpgrade → EnsureEtcdUpgrade → EnsureMasterUpgrade → EnsureWorkerUpgrade → EnsureComponentUpgrade | → Running (成功) / UpgradeFailed (失败) |
| **StateScaling** | 节点扩缩容 | Master/Worker 节点数量变化 | EnsureMasterJoin/EnsureMasterDelete/EnsureWorkerJoin/EnsureWorkerDelete（根据方向动态选择） | → Running (成功) / ScaleFailed (失败) |
| **StateManaging** | 纳管现有集群 | 纳管外部已有集群 | EnsureClusterManage | → Running (成功) / ManageFailed (失败) |
| **StateDeleting** | 删除中 | DeletionTimestamp 非空 或 Spec.Reset=true | EnsurePaused → EnsureDeleteOrReset | → None (成功删除) / DeleteFailed (失败) |
| **StatePausing** | 暂停中 | Spec.Pause=true | EnsurePaused | → Running (成功) / PauseFailed (失败) |
| **StateDryRunning** | DryRun 模式 | Spec.DryRun=true | EnsureDryRun | → Running (成功) / DryRunFailed (失败) |
| **StateInitFailed** | 初始化失败 | Provisioning 阶段失败 | 无（等待重试或人工修复） | → Provisioning (重试) |
| **StateUpgradeFailed** | 升级失败 | Upgrading 阶段失败 | 无（等待重试或人工修复） | → Upgrading (重试) |
| **StateScaleFailed** | 扩缩容失败 | Scaling 阶段失败 | 无（等待重试或人工修复） | → Scaling (重试) |
| **StateManageFailed** | 纳管失败 | Managing 阶段失败 | 无（等待重试或人工修复） | → Managing (重试) |
| **StateDeleteFailed** | 删除失败 | Deleting 阶段失败 | 无（等待重试或人工修复） | → Deleting (重试) |
| **StatePauseFailed** | 暂停失败 | Pausing 阶段失败 | 无（等待重试或人工修复） | → Pausing (重试) |
| **StateDryRunFailed** | DryRun 失败 | DryRunning 阶段失败 | 无（等待重试或人工修复） | → DryRunning (重试) |
### 二、状态转换图
```
                                    ┌─────────────────────────────────────────────────────────────────┐
                                    │                        StateNone (初始)                         │
                                    └───────────────────────────┬─────────────────────────────────────┘
                                                                │
                    ┌───────────────┬───────────────┬──────────┼──────────┬───────────────┬───────────────┐
                    │               │               │          │          │               │               │
                    ▼               ▼               ▼          ▼          ▼               ▼               ▼
           ┌──────────────┐ ┌──────────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
           │ Provisioning │ │  Managing    │ │ Deleting │ │ Pausing  │ │DryRunning│ │ Upgrading│ │ Scaling  │
           └──────┬───────┘ └──────┬───────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘
                  │                │              │            │            │            │            │
           ┌──────┴──────┐   ┌─────┴─────┐   ┌────┴────┐  ┌────┴────┐  ┌────┴────┐  ┌────┴────┐  ┌────┴────┐
           │             │   │           │   │         │  │         │  │         │  │         │  │         │
           ▼             ▼   ▼           ▼   ▼         │  ▼         │  ▼         │  ▼         │  ▼         │
    ┌──────────┐  ┌──────────┐ ┌────────┐ ┌────────┐   │  ┌────────┐ │  ┌────────┐ │  ┌────────┐ │  ┌────────┐
    │ Running  │  │InitFailed│ │Running │ │Manage  │   │  │Running │ │  │Running │ │  │Running │ │  │Running │
    └────┬─────┘  └────┬─────┘ └───┬────┘ │Failed  │   │  └───┬────┘ │  └───┬────┘ │  └───┬────┘ │  └───┬────┘
         │             │ retry     │      └───┬────┘   │      │      │      │      │      │      │      │
         │             └───────────┘          │        │      │      │      │      │      │      │      │
         │                    ┌───────────────┘        │      │      │      │      │      │      │      │
         │                    │                        │      │      │      │      │      │      │      │
         │                    ▼                        │      ▼      │      ▼      │      ▼      │      ▼
         │             ┌──────────────┐                │ ┌───────────┐ ┌──────────┐ ┌───────────┐ ┌───────────┐
         │             │   Running    │◄───────────────┘ │PauseFailed│ │DryRunFail│ │UpgradeFail│ │ScaleFailed│
         │             └──────────────┘                  └────┬──────┘ └────┬─────┘ └────┬──────┘ └────┬──────┘
         │                                                     │            │            │            │
         │                                                     │ retry      │ retry      │ retry      │ retry
         │                                                     ▼            ▼            ▼            ▼
         │                                              ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐
         │                                              │ Pausing  │  │DryRunning│  │ Upgrading│  │ Scaling  │
         │                                              └──────────┘  └──────────┘  └──────────┘  └──────────┘
         │
         │  用户操作触发状态转换
         │
    ┌────┴──────────────────────────────────────────────────────────────────────────────────────────────────┐
    │                                                                                                       │
    │   ┌────────────────┐                                                                                  │
    │   │    Running     │                                                                                  │
    │   └───────┬────────┘                                                                                  │
    │           │                                                                                           │
    │   ┌───────┼───────┬───────────┬───────────┬───────────┬───────────┐                                   │
    │   │       │       │           │           │           │           │                                   │
    │   ▼       ▼       ▼           ▼           ▼           ▼           ▼                                   │
    │ K8s版本  节点数  删除请求    暂停请求   DryRun请求   纳管请求    健康检查                             │
    │   变化    变化                                                                                        │
    │   │       │       │           │           │           │           │                                   │
    │   ▼       ▼       ▼           ▼           ▼           ▼           ▼                                   │
    │ Upgrading Scaling Deleting   Pausing   DryRunning  Managing   EnsureCluster                           │
    │                                                                                                       │
    └───────────────────────────────────────────────────────────────────────────────────────────────────────┘
```
### 三、与现有 ClusterStatus 的映射关系
当前系统使用 `ClusterStatus`（定义在 [bkecluster_status.go](file:///d:/code/github/cluster-api-provider-bke/api/bkecommon/v1beta1/bkecluster_status.go)）表示集群操作状态。状态机的 `ClusterLifecycleState` 与现有 `ClusterStatus` 是**双向映射**关系：
#### 3.1 ClusterStatus → ClusterLifecycleState 映射
| ClusterStatus | ClusterLifecycleState | 说明 |
|---------------|----------------------|------|
| `ClusterInitializing` | `StateProvisioning` | 集群初始化中 |
| `ClusterReady` | `StateRunning` | 集群就绪 |
| `ClusterUpgrading` | `StateUpgrading` | 版本升级中 |
| `ClusterMasterScalingUp` | `StateScaling` | Master 扩容 |
| `ClusterMasterScalingDown` | `StateScaling` | Master 缩容 |
| `ClusterWorkerScalingUp` | `StateScaling` | Worker 扩容 |
| `ClusterWorkerScalingDown` | `StateScaling` | Worker 缩容 |
| `ClusterManaging` | `StateManaging` | 纳管中 |
| `ClusterDeleting` | `StateDeleting` | 删除中 |
| `ClusterPaused` | `StatePausing` | 暂停中（注：暂停成功后状态保持） |
| `ClusterDryRun` | `StateDryRunning` | DryRun 中 |
| `ClusterInitializationFailed` | `StateInitFailed` | 初始化失败 |
| `ClusterUpgradeFailed` | `StateUpgradeFailed` | 升级失败 |
| `ClusterScaleFailed` | `StateScaleFailed` | 扩缩容失败 |
| `ClusterManageFailed` | `StateManageFailed` | 纳管失败 |
| `ClusterDeleteFailed` | `StateDeleteFailed` | 删除失败 |
| `ClusterPauseFailed` | `StatePauseFailed` | 暂停失败 |
| `ClusterDryRunFailed` | `StateDryRunFailed` | DryRun 失败 |
| `ClusterUnknown` | `StateNone` | 未知状态，重新判断 |
#### 3.2 ClusterLifecycleState → ClusterStatus 映射
```go
func (sm *ClusterStateMachine) syncClusterStatus(ctx *StateContext) {
    status := ctx.BKECluster.Status
    
    status.ClusterStatus = confv1beta1.ClusterStatus(sm.currentState)
    
    switch sm.currentState {
    case StateProvisioning:
        status.ClusterHealthState = confv1beta1.Deploying
    case StateRunning:
        status.ClusterHealthState = confv1beta1.Healthy
        status.Ready = true
    case StateUpgrading:
        status.ClusterHealthState = confv1beta1.Upgrading
    case StateScaling:
        // 保持原有的具体扩缩容状态
    case StateDeleting:
        status.ClusterHealthState = confv1beta1.Deleting
    case StateInitFailed:
        status.ClusterHealthState = confv1beta1.DeployFailed
    case StateUpgradeFailed:
        status.ClusterHealthState = confv1beta1.UpgradeFailed
    case StateManageFailed:
        status.ClusterHealthState = confv1beta1.ManageFailed
    }
}
```
### 四、状态转换触发条件
#### 4.1 从 StateNone 触发的转换
| 触发条件 | 目标状态 | Event | 说明 |
|---------|---------|-------|------|
| `DeletionTimestamp != nil` | `StateDeleting` | `delete` | 集群被删除 |
| `Spec.Reset == true` | `StateDeleting` | `delete` | 重置集群 |
| `Spec.Pause == true` | `StatePausing` | `pause` | 暂停请求 |
| `Spec.DryRun == true` | `StateDryRunning` | `dryrun` | DryRun 请求 |
| `Status.Ready == false && DeletionTimestamp == nil` | `StateProvisioning` | `provision` | 新集群部署 |
| `Spec.ClusterConfig == nil && Status.Ready == false` | `StateManaging` | `manage` | 纳管现有集群 |
#### 4.2 从 StateRunning 触发的转换
| 触发条件 | 目标状态 | Event | 说明 |
|---------|---------|-------|------|
| `DeletionTimestamp != nil` | `StateDeleting` | `delete` | 删除请求 |
| `Spec.Reset == true` | `StateDeleting` | `delete` | 重置请求 |
| `Spec.Pause == true` | `StatePausing` | `pause` | 暂停请求 |
| `Spec.DryRun == true` | `StateDryRunning` | `dryrun` | DryRun 请求 |
| `KubernetesVersion 变化` | `StateUpgrading` | `upgrade` | K8s 版本升级 |
| `OpenFuyaoVersion 变化` | `StateUpgrading` | `upgrade` | OpenFuyao 版本升级 |
| `ContainerdVersion 变化` | `StateUpgrading` | `upgrade` | Containerd 版本升级 |
| `Master 节点数增加` | `StateScaling` | `scale` | Master 扩容 |
| `Master 节点数减少` | `StateScaling` | `scale` | Master 缩容 |
| `Worker 节点数增加` | `StateScaling` | `scale` | Worker 扩容 |
| `Worker 节点数减少` | `StateScaling` | `scale` | Worker 缩容 |
#### 4.3 失败状态的重试转换
所有失败状态都可以通过 `retry` 事件转换回对应的运行中状态：

| 失败状态 | 重试目标状态 | Event |
|---------|-------------|-------|
| `StateInitFailed` | `StateProvisioning` | `retry` |
| `StateUpgradeFailed` | `StateUpgrading` | `retry` |
| `StateScaleFailed` | `StateScaling` | `retry` |
| `StateManageFailed` | `StateManaging` | `retry` |
| `StateDeleteFailed` | `StateDeleting` | `retry` |
| `StatePauseFailed` | `StatePausing` | `retry` |
| `StateDryRunFailed` | `StateDryRunning` | `retry` |
### 五、状态机与现有 PhaseFlow 的对比
| 维度 | PhaseFlow（现有） | StateMachine（重构后） |
|------|------------------|----------------------|
| **状态表示** | `ClusterStatus` + `PhaseStatus` 分离 | `ClusterLifecycleState` 统一状态模型 |
| **状态转换** | 隐式（通过 Phase 执行结果推断） | 显式（通过转换表定义） |
| **转换验证** | 无验证 | `CanTransitionTo` 验证合法性 |
| **执行模型** | 两阶段（Calculate + Execute） | 单阶段（Reconcile 中完成） |
| **错误处理** | 聚合所有错误后返回 | 按步骤返回，支持重试标记 |
| **状态持久化** | Phase.Report 写入 PhaseStatus | State.Enter/Exit 同步 ClusterStatus |
| **可观测性** | 需查看 PhaseStatus 列表 | 状态机状态直接反映集群生命周期 |
### 六、状态持久化策略
#### 6.1 状态存储位置
```go
type BKEClusterStatus struct {
    // 集群级别状态（状态机状态）
    ClusterStatus ClusterStatus `json:"clusterStatus,omitempty"`
    
    // 集群健康状态（辅助状态）
    ClusterHealthState ClusterHealthState `json:"clusterHealthState,omitempty"`
    
    // Phase 级别状态（保留，用于 UI 展示进度）
    PhaseStatus PhaseStatus `json:"phaseStatus,omitempty"`
    
    // 阶段一新增：升级状态跟踪
    UpgradeStatus *UpgradeStatus `json:"upgradeStatus,omitempty"`
}
```
#### 6.2 状态同步时机
| 时机 | 操作 | 说明 |
|------|------|------|
| `State.Enter()` | 设置 `ClusterStatus` 为当前状态 | 进入新状态时立即同步 |
| `State.Exit()` | 清理临时状态 | 退出状态时清理 |
| Phase 执行成功 | 更新 `PhaseStatus` | 保留现有 Phase 进度展示 |
| 状态转换完成 | 更新 `ClusterHealthState` | 同步健康状态 |
### 七、特殊场景处理
#### 7.1 暂停恢复
```
StateRunning ──pause──► StatePausing ──success──► StateRunning
                              │
                              │ fail
                              ▼
                       StatePauseFailed
                              │
                              │ retry
                              ▼
                         StatePausing
```
**说明**：暂停成功后集群回到 `StateRunning`，但 `Spec.Pause=true` 保持。下次 Reconcile 时会检测到暂停请求已完成，不再触发状态转换。
#### 7.2 删除流程
```
任意状态 ──delete──► StateDeleting ──success──► StateNone (资源已删除)
                            │
                            │ fail
                            ▼
                     StateDeleteFailed
                            │
                            │ retry
                            ▼
                       StateDeleting
```
**说明**：删除成功后资源被 GC 回收，状态机状态无意义。删除失败时需要人工介入或重试。
#### 7.3 并发操作冲突
当用户在 `StateUpgrading` 过程中请求删除时：
```
StateUpgrading ──delete──► ???
```
**处理策略**：状态转换表不允许 `Upgrading → Deleting` 直接转换。需要：
1. 等待升级完成或失败
2. 或者强制取消升级（需要额外实现取消机制）

当前设计采用**保守策略**：不允许跨类型状态转换，确保操作原子性。

/******************************************************************
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

// package for controller bkeagent
package bkeagent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	apierr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	bkenet "gopkg.openfuyao.cn/cluster-api-provider-bke/common/utils/net"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

// CommandReconciler reconciles a Command object
type CommandReconciler struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Ctx       context.Context
	Job       job.Job
	NodeName  string
	NodeIP    string
}

const (
	// commandFinalizerName 是 Command 资源的 finalizer 名称
	commandFinalizerName = "command.bkeagent.bocloud.com/finalizers"
)

const (
	defaultFastDelay       = 10 * time.Second
	defaultSlowDelay       = 60 * time.Second
	defaultMaxFastAttempts = 5
)

// reconcileResult 封装 Reconcile 函数的返回结果
type reconcileResult struct {
	result ctrl.Result
	err    error
	done   bool
}

// continueReconcile 表示继续执行后续逻辑
func continueReconcile() reconcileResult {
	return reconcileResult{done: false}
}

// finishReconcile 表示应该立即返回
func finishReconcile(result ctrl.Result, err error) reconcileResult {
	return reconcileResult{result: result, err: err, done: true}
}

// finishWithRequeue 表示需要重新入队
func finishWithRequeue() reconcileResult {
	return reconcileResult{result: ctrl.Result{Requeue: true}, done: true}
}

// unwrap 解包返回 ctrl.Result 和 error
func (r reconcileResult) unwrap() (ctrl.Result, error) {
	return r.result, r.err
}

// +kubebuilder:rbac:groups=bkeagent.bocloud.com,resources=commands,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bkeagent.bocloud.com,resources=commands/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=bkeagent.bocloud.com,resources=commands/finalizers,verbs=update
// Reconcile command
func (r *CommandReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// 1. 获取 Command 对象
	command, res := r.fetchCommand(ctx, req)
	if res.done {
		return res.result, res.err
	}

	// 2. Commands 为空检查
	if len(command.Spec.Commands) == 0 {
		log.Warnf("command is not configured, %s/%s", command.Namespace, command.Name)
		return ctrl.Result{}, nil
	}

	log.Infof("reconcile %s/%s resourceVersion: %s, generation: %d",
		command.Namespace, command.Name, command.GetResourceVersion(), command.GetGeneration())

	// 3. 初始化 Status
	if res := r.ensureStatusInitialized(command); res.done {
		return res.result, res.err
	}

	currentStatus := command.Status[r.commandStatusKey()]
	gid := fmt.Sprintf("%s/%s", command.Namespace, command.Name)

	// 4. 处理 Finalizer
	if res := r.handleFinalizer(ctx, command, gid); res.done {
		return res.result, res.err
	}

	// 5. 跳过已完成的命令
	if currentStatus.Phase == agentv1beta1.CommandComplete {
		return ctrl.Result{}, nil
	}

	// 6. 处理暂停逻辑
	if res := r.handleSuspend(command, currentStatus, gid); res.done {
		return res.result, res.err
	}

	// 7. 跳过旧版本任务
	if r.shouldSkipOldTask(command, gid) {
		return ctrl.Result{}, nil
	}

	// 8. 创建并启动任务
	return r.createAndStartTask(ctx, command, currentStatus, gid).unwrap()
}

// fetchCommand 获取 Command 对象
// - ctx: 上下文
// - req: reconcile 请求，包含 NamespacedName
// - *agentv1beta1.Command: 获取到的 Command 对象，如果不存在则为 nil
// - reconcileResult: 包含是否需要返回以及具体的返回值
func (r *CommandReconciler) fetchCommand(ctx context.Context,
	req ctrl.Request) (*agentv1beta1.Command, reconcileResult) {
	command := new(agentv1beta1.Command)
	if err := r.Get(ctx, req.NamespacedName, command); err != nil {
		if apierr.IsNotFound(err) {
			return nil, finishReconcile(ctrl.Result{}, nil)
		}
		log.Errorf("unable to fetch Command, %s", err.Error())
		return nil, finishReconcile(ctrl.Result{}, err)
	}
	return command, continueReconcile()
}

// ensureStatusInitialized 确保 Command 的 Status 已初始化
// - command: Command 对象
// - reconcileResult: 如果初始化失败需要返回，否则继续
func (r *CommandReconciler) ensureStatusInitialized(command *agentv1beta1.Command) reconcileResult {
	if command.Status == nil {
		command.Status = map[string]*agentv1beta1.CommandStatus{}
	}
	if _, ok := command.Status[r.commandStatusKey()]; !ok {
		command.Status[r.commandStatusKey()] = &agentv1beta1.CommandStatus{
			Conditions:     []*agentv1beta1.Condition{},
			LastStartTime:  &metav1.Time{Time: time.Now()},
			CompletionTime: nil,
			Succeeded:      -1,
			Failed:         -1,
			Phase:          agentv1beta1.CommandRunning,
			Status:         metav1.ConditionUnknown,
		}
		if err := r.syncStatusUntilComplete(command); err != nil {
			log.Errorf("unable to update command status resourceVersion: %s error: %s",
				command.GetResourceVersion(), err.Error())
			return finishReconcile(ctrl.Result{}, nil)
		}
	}
	return continueReconcile()
}

// handleUpdateError 处理 Update 操作的错误
// 统一处理 Conflict、NotFound 和其他错误
func handleUpdateError(err error) reconcileResult {
	if apierr.IsConflict(err) {
		return finishWithRequeue()
	}
	if apierr.IsNotFound(err) {
		return finishReconcile(ctrl.Result{}, nil)
	}
	return finishReconcile(ctrl.Result{}, err)
}

// cleanupTask 清理指定 gid 的任务
func (r *CommandReconciler) cleanupTask(gid string) {
	if v, ok := r.Job.Task[gid]; ok {
		v.SafeClose()
		delete(r.Job.Task, gid)
	}
}

// ensureFinalizer 确保 Command 对象包含 finalizer
// 返回 continueReconcile 如果 finalizer 已存在或添加成功
// 返回 finishReconcile 或 finishWithRequeue 如果更新失败
func (r *CommandReconciler) ensureFinalizer(ctx context.Context,
	command *agentv1beta1.Command) reconcileResult {
	if controllerutil.ContainsFinalizer(command, commandFinalizerName) {
		return continueReconcile()
	}

	controllerutil.AddFinalizer(command, commandFinalizerName)
	if err := r.Update(ctx, command); err != nil {
		return handleUpdateError(err)
	}
	return continueReconcile()
}

// handleDeletion 处理 Command 删除时的清理工作
// 清理任务并移除 finalizer
func (r *CommandReconciler) handleDeletion(ctx context.Context,
	command *agentv1beta1.Command, gid string) reconcileResult {
	if !controllerutil.ContainsFinalizer(command, commandFinalizerName) {
		return finishReconcile(ctrl.Result{}, nil)
	}

	r.cleanupTask(gid)
	controllerutil.RemoveFinalizer(command, commandFinalizerName)

	if err := r.Update(ctx, command); err != nil {
		if apierr.IsConflict(err) {
			log.Warnf("conflict when update command, %s", err.Error())
		}
		return handleUpdateError(err)
	}

	log.Infof("The Finalizer is removed %s", gid)
	return finishReconcile(ctrl.Result{}, nil)
}

// handleFinalizer 处理 Finalizer 的添加和删除
// - ctx: 上下文
// - command: Command 对象
// - gid: 全局任务 ID
// - reconcileResult: 处理结果
func (r *CommandReconciler) handleFinalizer(ctx context.Context,
	command *agentv1beta1.Command, gid string) reconcileResult {
	// 非删除状态：确保 finalizer 存在
	if command.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.ensureFinalizer(ctx, command)
	}
	// 删除状态：清理并移除 finalizer
	return r.handleDeletion(ctx, command, gid)
}

// handleSuspend 处理命令暂停逻辑
// - command: Command 对象
// - currentStatus: 当前节点的状态
// - gid: 全局任务 ID
// - reconcileResult: 如果已暂停或需要暂停，返回 done=true
func (r *CommandReconciler) handleSuspend(command *agentv1beta1.Command,
	currentStatus *agentv1beta1.CommandStatus, gid string) reconcileResult {
	if !command.Spec.Suspend {
		return continueReconcile()
	}

	if currentStatus.Phase == agentv1beta1.CommandSuspend {
		return finishReconcile(ctrl.Result{}, nil)
	}

	log.Infof("%s has been suspended", gid)
	if v, ok := r.Job.Task[gid]; ok {
		v.SafeClose()
		v.Phase = agentv1beta1.CommandSuspend
	}

	// Statistical state
	countResult := agentv1beta1.ConditionCount(currentStatus.Conditions, len(command.Spec.Commands))
	currentStatus.Succeeded, currentStatus.Failed, currentStatus.Status, currentStatus.Phase =
		countResult.Succeeded, countResult.Failed, countResult.Status, countResult.Phase
	currentStatus.Phase = agentv1beta1.CommandSuspend

	if err := r.syncStatusUntilComplete(command); err != nil {
		log.Errorf("unable to update command status resourceVersion: %s error: %s",
			command.GetResourceVersion(), err.Error())
		return finishReconcile(ctrl.Result{}, err)
	}
	return finishReconcile(ctrl.Result{}, nil)
}

// shouldSkipOldTask 检查是否应该跳过旧版本任务
// - command: Command 对象
// - gid: 全局任务 ID
// - bool: 如果应该跳过返回 true
func (r *CommandReconciler) shouldSkipOldTask(command *agentv1beta1.Command, gid string) bool {
	if v, ok := r.Job.Task[gid]; ok {
		// This value is increased by a spec change
		if command.GetGeneration() <= v.Generation {
			log.Infof("A later version task is being executed, command:%s resourceVersion:%s-%s, generation:%d<=%d",
				gid, command.GetResourceVersion(), v.ResourceVersion, command.GetGeneration(), v.Generation)
			return true
		}
		v.SafeClose()
	}
	return false
}

// createAndStartTask 创建新任务并启动执行
// - ctx: 上下文
// - command: Command 对象
// - currentStatus: 当前节点的状态
// - gid: 全局任务 ID
// - reconcileResult: 任务创建结果
func (r *CommandReconciler) createAndStartTask(ctx context.Context, command *agentv1beta1.Command,
	currentStatus *agentv1beta1.CommandStatus, gid string) reconcileResult {
	r.Job.Task[gid] = &job.Task{
		StopChan:                make(chan struct{}),
		Phase:                   agentv1beta1.CommandRunning,
		ResourceVersion:         command.ResourceVersion,
		Generation:              command.GetGeneration(),
		TTLSecondsAfterFinished: command.Spec.TTLSecondsAfterFinished,
		HasAddTimer:             false,
		Once:                    &sync.Once{},
	}

	// The start time is reset each time the Reconcile function is entered
	currentStatus.LastStartTime = &metav1.Time{Time: time.Now()}
	currentStatus.CompletionTime = nil

	if err := r.syncStatusUntilComplete(command); err != nil {
		log.Errorf("unable to update command status resourceVersion: %s error: %s",
			command.GetResourceVersion(), err.Error())
		return finishReconcile(ctrl.Result{}, nil)
	}

	go r.startTask(ctx, r.Job.Task[gid].StopChan, command)
	return finishReconcile(ctrl.Result{}, nil)
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommandReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Clears resource objects with TTL set
	go r.ttlSecondAfterFinished()

	return ctrl.NewControllerManagedBy(mgr).
		For(&agentv1beta1.Command{}, r.commandPredicateFn()).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemFastSlowRateLimiter(defaultFastDelay, defaultSlowDelay, defaultMaxFastAttempts),
		}).Complete(r)
}

// shouldReconcileCommand 判断是否应该触发 Reconcile
// eventType 用于日志标识事件来源 (CreateFunc/UpdateFunc)
func (r *CommandReconciler) shouldReconcileCommand(o *agentv1beta1.Command, eventType string) bool {
	if o == nil {
		return false
	}
	gid := fmt.Sprintf("%s/%s", o.Namespace, o.Name)
	if v, ok := r.Job.Task[gid]; ok {
		// The value of this field will increase only if you update the spec
		if o.Generation <= v.Generation || o.ResourceVersion <= v.ResourceVersion {
			log.Debugf("%s: %d<=%d Only update status without Reconcile %s",
				eventType, o.Generation, v.Generation, gid)
			return false
		}
	}
	// todo 废弃Spec.NodeName，可承载信息太少
	if o.Spec.NodeName == r.NodeName {
		return true
	}
	return r.nodeMatchNodeSelector(o.Spec.NodeSelector)
}

func (r *CommandReconciler) commandPredicateFn() builder.Predicates {
	commandPredicateFn := builder.WithPredicates(
		predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				cmd, ok := e.Object.(*agentv1beta1.Command)
				if !ok {
					return false
				}
				return r.shouldReconcileCommand(cmd, "CreateFunc")
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				cmd, ok := e.ObjectNew.(*agentv1beta1.Command)
				if !ok {
					return false
				}
				return r.shouldReconcileCommand(cmd, "UpdateFunc")
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		},
	)
	return commandPredicateFn
}

// ========== startTask 辅助函数 ==========

// calculateStopTime 根据命令配置计算任务的截止时间
func calculateStopTime(lastStartTime time.Time, activeDeadlineSecond int) time.Time {
	if activeDeadlineSecond > 0 {
		return lastStartTime.Add(time.Duration(activeDeadlineSecond) * time.Second)
	}
	return lastStartTime.Add(time.Duration(agentv1beta1.DefaultActiveDeadlineSecond) * time.Second)
}

// isCommandCompleted 检查指定命令是否已经执行完成
func isCommandCompleted(conditions []*agentv1beta1.Condition, commandID string) bool {
	cond := agentv1beta1.GetCondition(conditions, &agentv1beta1.Condition{ID: commandID})
	return cond != nil && cond.Phase == agentv1beta1.CommandComplete
}

// newCondition 为执行命令创建新的 Condition 对象
func newCondition(execCommandID string) *agentv1beta1.Condition {
	return &agentv1beta1.Condition{
		ID:            execCommandID,
		Status:        metav1.ConditionUnknown,
		Phase:         agentv1beta1.CommandRunning,
		LastStartTime: &metav1.Time{Time: time.Now()},
		StdErr:        []string{},
		StdOut:        []string{},
		Count:         0,
	}
}

// executeByType 根据命令类型路由到相应的执行器
// 注意：对于不支持的命令类型，只打印日志并返回 nil, nil（与旧版本行为一致）
func (r *CommandReconciler) executeByType(cmdType agentv1beta1.CommandType, command []string) ([]string, error) {
	switch cmdType {
	case agentv1beta1.CommandBuiltIn:
		return r.Job.BuiltIn.Execute(command)
	case agentv1beta1.CommandKubernetes:
		return r.Job.K8s.Execute(command)
	case agentv1beta1.CommandShell:
		return r.Job.Shell.Execute(command)
	default:
		log.Errorf("Unsupported command type: %s", cmdType)
		return nil, nil
	}
}

// commandExecutionResult 封装命令执行的结果
type commandExecutionResult struct {
	timedOut bool
}

// executeWithRetry 执行单个命令，支持重试机制
func (r *CommandReconciler) executeWithRetry(execCommand agentv1beta1.ExecCommand,
	condition *agentv1beta1.Condition, stopTime time.Time, backoffLimit int) commandExecutionResult {

	for backoffLimit >= 0 && condition.Count <= backoffLimit {
		if stopTime.Before(time.Now()) {
			return commandExecutionResult{timedOut: true}
		}
		// 重试时添加延迟
		if execCommand.BackoffDelay != 0 && condition.Count > 0 {
			time.Sleep(time.Duration(execCommand.BackoffDelay) * time.Second)
		}
		condition.LastStartTime = &metav1.Time{Time: time.Now()}
		condition.Count++

		result, err := r.executeByType(execCommand.Type, execCommand.Command)
		if err != nil {
			condition.Status = metav1.ConditionFalse
			condition.Phase = agentv1beta1.CommandFailed
			condition.StdErr = append(condition.StdErr, err.Error())
			log.Errorf("Command exec failed: %s %s", condition.ID, err.Error())
			continue
		}
		condition.Status = metav1.ConditionTrue
		condition.Phase = agentv1beta1.CommandComplete
		condition.StdOut = append(condition.StdOut, result...)
		break
	}
	return commandExecutionResult{timedOut: false}
}

// processCommandResult 封装单个命令处理的结果
type processCommandResult struct {
	shouldBreak bool
	syncError   error
}

// processExecCommand 处理单个执行命令的完整流程
func (r *CommandReconciler) processExecCommand(command *agentv1beta1.Command,
	execCommand agentv1beta1.ExecCommand, currentStatus *agentv1beta1.CommandStatus,
	stopTime time.Time) processCommandResult {

	condition := newCondition(execCommand.ID)
	currentStatus.Conditions = agentv1beta1.ReplaceCondition(currentStatus.Conditions, condition)

	result := r.executeWithRetry(execCommand, condition, stopTime, command.Spec.BackoffLimit)

	// 如果配置了忽略，命令失败也可以跳过
	if condition.Status == metav1.ConditionFalse && execCommand.BackoffIgnore {
		condition.Phase = agentv1beta1.CommandSkip
	}

	// 更新每个子指令执行后的状态
	if err := r.syncStatusUntilComplete(command); err != nil {
		return processCommandResult{syncError: err}
	}

	// 如果最终状态是执行失败，停止后续指令的执行
	shouldBreak := !result.timedOut && condition.Phase == agentv1beta1.CommandFailed
	return processCommandResult{shouldBreak: shouldBreak}
}

// finalizeTaskStatus 统计并更新任务的最终状态
func (r *CommandReconciler) finalizeTaskStatus(command *agentv1beta1.Command,
	currentStatus *agentv1beta1.CommandStatus, gid string) error {

	countResult := agentv1beta1.ConditionCount(currentStatus.Conditions, len(command.Spec.Commands))
	currentStatus.Succeeded = countResult.Succeeded
	currentStatus.Failed = countResult.Failed
	currentStatus.Status = countResult.Status
	currentStatus.Phase = countResult.Phase
	currentStatus.CompletionTime = &metav1.Time{Time: time.Now()}

	command.Status[r.commandStatusKey()] = currentStatus
	if err := r.syncStatusUntilComplete(command); err != nil {
		return err
	}

	if v, ok := r.Job.Task[gid]; ok {
		v.Phase = currentStatus.Phase
		v.SafeClose()
	}
	return nil
}

func (r *CommandReconciler) startTask(ctx context.Context, stopChan chan struct{}, command *agentv1beta1.Command) {
	gid := fmt.Sprintf("%s/%s", command.Namespace, command.Name)
	currentStatus := command.Status[r.commandStatusKey()]
	stopTime := calculateStopTime(currentStatus.LastStartTime.Time, command.Spec.ActiveDeadlineSecond)

	terminated := false
	for _, execCommand := range command.Spec.Commands {
		// 检查是否收到停止信号
		select {
		case <-stopChan:
			log.Warnf("Execution command terminated %s", gid)
			terminated = true
		default:
		}
		if terminated {
			return // 停止信号，直接返回，不执行 finalizeTaskStatus
		}
		// 检查是否超时
		if stopTime.Before(time.Now()) {
			break
		}
		if isCommandCompleted(currentStatus.Conditions, execCommand.ID) {
			continue
		}

		result := r.processExecCommand(command, execCommand, currentStatus, stopTime)
		if result.syncError != nil {
			log.Errorf("unable to update command status resourceVersion: %s error: %s",
				command.ResourceVersion, result.syncError.Error())
			return
		}
		if result.shouldBreak {
			break
		}
	}

	if err := r.finalizeTaskStatus(command, currentStatus, gid); err != nil {
		log.Errorf("unable to update command status resourceVersion: %s error: %s",
			command.ResourceVersion, err.Error())
	}
}

func (r *CommandReconciler) commandStatusKey() string {
	if r.NodeIP == "" {
		return r.NodeName
	}
	return fmt.Sprintf("%s/%s", r.NodeName, r.NodeIP)
}

func (r *CommandReconciler) ttlSecondAfterFinished() {
	const sleepRangeLow, sleepRangeHigh = 30, 60
	for {
		select {
		case <-r.Ctx.Done():
			return
		default:
		}
		time.Sleep(time.Duration(rand.IntnRange(sleepRangeLow, sleepRangeHigh)) * time.Second)
		for key, value := range r.Job.Task {
			r.processTTLTask(key, value)
		}
	}
}

// shouldProcessTask checks if a task should be processed for TTL deletion
func (r *CommandReconciler) shouldProcessTask(value *job.Task) bool {
	return !value.HasAddTimer && value.TTLSecondsAfterFinished != 0 && value.Phase == agentv1beta1.CommandComplete
}

// isCommandReadyForDeletion checks if all nodes have completed the command
func (r *CommandReconciler) isCommandReadyForDeletion(obj *agentv1beta1.Command, key string) bool {
	for n, v := range obj.Status {
		if v.Status != metav1.ConditionTrue {
			log.Warnf("Instruction %s not completed on node %s, refused to delete", key, n)
			return false
		}
	}
	return true
}

// calculateTTL computes the remaining TTL based on completion time
func (r *CommandReconciler) calculateTTL(ttlSeconds int, completionTime time.Time) int {
	const ttlRangeLow, ttlRangeHigh = 0, 3
	newTTL := ttlSeconds - int(time.Since(completionTime).Seconds())
	if newTTL <= 0 {
		return rand.IntnRange(ttlRangeLow, ttlRangeHigh)
	}
	return newTTL
}

// scheduleCommandDeletion schedules the deletion of a Command resource
func (r *CommandReconciler) scheduleCommandDeletion(obj *agentv1beta1.Command, key string, ttl int) {
	log.Infof("Command Resource %s will be deleted in %d seconds", key, ttl)
	time.AfterFunc(time.Duration(ttl)*time.Second, func() {
		log.Infof("Deleting the Command resource %s/%s", obj.Namespace, obj.Name)
		if err := r.Delete(r.Ctx, obj); err != nil {
			log.Warnf("Command resource %s/%s deletion failed. %s", obj.Namespace, obj.Name, err.Error())
		}
	})
}

// processTTLTask handles the TTL processing for a single task
func (r *CommandReconciler) processTTLTask(key string, value *job.Task) {
	if !r.shouldProcessTask(value) {
		return
	}
	namespace, name, _ := cache.SplitMetaNamespaceKey(key)
	obj := &agentv1beta1.Command{}
	if err := r.Client.Get(r.Ctx, client.ObjectKey{Namespace: namespace, Name: name}, obj); err != nil {
		if apierr.IsNotFound(err) {
			delete(r.Job.Task, key)
		} else {
			log.Warnf("unable fetch command %s", err.Error())
		}
		return
	}
	if !r.isCommandReadyForDeletion(obj, key) {
		return
	}
	value.HasAddTimer = true
	ttl := r.calculateTTL(value.TTLSecondsAfterFinished, obj.Status[r.commandStatusKey()].CompletionTime.Time)
	r.scheduleCommandDeletion(obj, key, ttl)
}

func (r *CommandReconciler) syncStatusUntilComplete(cmd *agentv1beta1.Command) (err error) {
	const timeout = 5 * time.Minute
	ctx, cancel := context.WithTimeout(r.Ctx, timeout)
	defer cancel()
	for {
		select {
		case <-r.Ctx.Done():
			return
		case <-ctx.Done():
			return errors.New("The update failed to complete after 5 minutes. ")
		default:
		}
		// Execute concurrent tasks at different peaks.
		// When the number of concurrent tasks is greater than 100, a random value of 1-15 is preferred
		const sleepRangeLow = 1
		const sleepRangeHigh = 2
		time.Sleep(time.Duration(rand.IntnRange(sleepRangeLow, sleepRangeHigh)) * time.Second)
		obj := &agentv1beta1.Command{}
		// This refresh is a direct request to the Kube-apiserver, which is equivalent to refreshing the local Client-go cache
		err = r.APIReader.Get(r.Ctx, client.ObjectKey{Namespace: cmd.Namespace, Name: cmd.Name}, obj)
		if err != nil {
			if apierr.IsNotFound(err) {
				log.Warnf("Command resource %s-%s not found, skip sync", cmd.Namespace, cmd.Name)
				return nil
			}
			log.Errorf("Get command resource failed %s-%s error: %v", cmd.Namespace, cmd.Name, err)
			continue
		}
		if obj.Status == nil {
			obj.Status = map[string]*agentv1beta1.CommandStatus{}
		}
		objCopy := obj.DeepCopy()
		objCopy.Status[r.commandStatusKey()] = cmd.Status[r.commandStatusKey()]
		//patch status
		err = r.Client.Status().Patch(r.Ctx, objCopy, client.MergeFrom(obj))
		if err != nil {
			log.Warnf("Update command resource failed %s-%s error: %v", cmd.Namespace, cmd.Name, err)
			continue
		}
		break
	}
	return nil
}

func (r *CommandReconciler) nodeMatchNodeSelector(s *metav1.LabelSelector) bool {
	if s == nil {
		return false
	}
	selector, err := metav1.LabelSelectorAsSelector(s)
	if err != nil {
		return false
	}
	// get node name from NodeSelector
	nodeName, found := selector.RequiresExactMatch(r.NodeName)
	if !found {
		nodeName = ""
	}
	if nodeName == r.NodeName {
		return true
	}

	// check ip exit in node interface ip
	ips, err := bkenet.GetAllInterfaceIP()
	if err != nil {
		return false
	}
	for _, p := range ips {
		tmpIP, _, err := net.ParseCIDR(p)
		if err != nil {
			continue
		}
		// skip localhost

		if ip, found := selector.RequiresExactMatch(tmpIP.String()); found {
			if tmpIP.String() == "127.0.0.1" || tmpIP.String() == "::1" {
				return false
			}
			if ip == tmpIP.String() {
				r.NodeIP = ip
				return true
			}
		}
	}
	return false
}

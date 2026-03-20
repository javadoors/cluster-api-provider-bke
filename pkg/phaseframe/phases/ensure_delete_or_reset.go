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

package phases

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/command"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	bkemetrics "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/metrics"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/statusmanage"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	agentutils "gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	EnsureDeleteOrResetName confv1beta1.BKEClusterPhase = "EnsureDeleteOrReset"
	// DeleteOrResetTimeoutMinutes 控制删除或重置操作的超时时间（分钟）
	DeleteOrResetTimeoutMinutes = 5
	// DeleteOrResetPollIntervalSeconds 控制删除或重置操作的轮询间隔（秒）
	DeleteOrResetPollIntervalSeconds = 10
	// ShutdownAgentWaitTimeoutSeconds 控制关闭代理命令的等待超时时间（秒）
	ShutdownAgentWaitTimeoutSeconds = 30
)

type EnsureDeleteOrReset struct {
	phaseframe.BasePhase
}

func NewEnsureDeleteOrReset(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureDeleteOrResetName)
	p := &EnsureDeleteOrReset{BasePhase: base}
	p.RegisterPostHooks(ensureDeleteOrResetPostHook)
	return p
}

func (e *EnsureDeleteOrReset) Execute() (ctrl.Result, error) {
	// 不使用PhaseCtx.Context，因为此时已经被cancel了
	baseCtx := context.Background()
	_, c, bkeCluster, _, log := e.Ctx.Untie()

	if e.Ctx.BKECluster.Spec.Pause {
		log.Info(constant.ClusterDeletingReason, "BKECluster %s is paused, resume it first", utils.ClientObjNS(e.Ctx.BKECluster))
		patchF := func(currentCombinedBKECluster *bkev1beta1.BKECluster) {
			currentCombinedBKECluster.Spec.Pause = false
		}
		if err := mergecluster.SyncStatusUntilComplete(e.Ctx.Client, e.Ctx.BKECluster, patchF); err != nil {
			return ctrl.Result{}, err
		}
		if err := e.Ctx.RefreshCtxBKECluster(baseCtx); err != nil {
			return ctrl.Result{}, err
		}

		// remove all command now
		log.Info(constant.ClusterDeletingReason, "cluster is paused, remove all agent command now")
		commandList := &agentv1beta1.CommandList{}
		filters := phaseutil.GetListFiltersByBKECluster(bkeCluster)
		if err := c.List(baseCtx, commandList, filters...); err != nil {
			log.Warn(constant.ReconcileErrorReason, "failed to list command: %v", err)
		} else {
			for _, command := range commandList.Items {
				// remove finalizer and delete
				controllerutil.RemoveFinalizer(&command, "command.bkeagent.bocloud.com/finalizers")
				if err = c.Update(baseCtx, &command); err != nil {
					if apierrors.IsNotFound(err) {
						continue
					}
					log.Warn(constant.ReconcileErrorReason, "failed to remove finalizer: %v", err)
				}
				if err = c.Delete(baseCtx, &command); err != nil {
					if apierrors.IsNotFound(err) {
						continue
					}
					log.Warn(constant.ReconcileErrorReason, "failed to delete command: %v", err)
				}
			}
		}

		return ctrl.Result{Requeue: true}, nil
	}

	ctx, cancel := context.WithTimeout(baseCtx, DeleteOrResetTimeoutMinutes*time.Minute)
	defer cancel()
	err := wait.PollImmediateUntil(DeleteOrResetPollIntervalSeconds*time.Second, func() (bool, error) {
		if err := e.reconcileDelete(ctx); err != nil {
			log.Warn("RetryDelete", "(ignore)reconcileDelete error, retry: %v", err)
			return false, nil
		}
		return true, nil
	}, ctx.Done())

	if errors.Is(err, wait.ErrWaitTimeout) {
		return ctrl.Result{}, errors.Errorf("Wait delete timeout")
	}
	return ctrl.Result{}, nil
}

func (e *EnsureDeleteOrReset) NeedExecute(_ *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !new.DeletionTimestamp.IsZero() || new.Spec.Reset {
		e.SetStatus(bkev1beta1.PhaseWaiting)
		return true
	}
	return false
}

func (e *EnsureDeleteOrReset) reconcileDelete(ctx context.Context) error {
	_, c, bkeCluster, _, log := e.Ctx.Untie()

	if err := e.Ctx.RefreshCtxCluster(ctx); err != nil {
		if !strings.Contains(err.Error(), "owner cluster is nil") {
			return err
		}
	}

	if err := e.ensureClusterStatusDeleting(c, bkeCluster, log); err != nil {
		return err
	}

	if err := e.handleClusterDeletion(ctx, c, log); err != nil {
		return err
	}

	if err := e.handleBKEMachineDeletion(ctx, c, bkeCluster, log); err != nil {
		return err
	}

	if err := e.deleteRelatedResources(ctx, c, bkeCluster, log); err != nil {
		return err
	}

	e.ShutDownAgent(ctx)

	if err := e.cleanupClusterResources(ctx, c, bkeCluster, log); err != nil {
		return err
	}

	return e.handleNamespaceDeletion(ctx, c, bkeCluster, log)
}

// ensureClusterStatusDeleting 确保集群状态为删除中
func (e *EnsureDeleteOrReset) ensureClusterStatusDeleting(c client.Client, bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger) error {
	if bkeCluster.Status.ClusterStatus != bkev1beta1.ClusterDeleting {
		log.Debug("mark bkeCluster as deleting")
		bkeCluster.Status.ClusterStatus = bkev1beta1.ClusterDeleting
		if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
			log.Warn(constant.ReconcileErrorReason, "failed to update bkeCluster Status: %v", err)
			return errors.Errorf("failed to update bkeCluster Status: %v", err)
		}
	}
	return nil
}

// handleClusterDeletion 处理集群删除
func (e *EnsureDeleteOrReset) handleClusterDeletion(ctx context.Context, c client.Client, log *bkev1beta1.BKELogger) error {
	if e.Ctx.Cluster != nil && e.Ctx.Cluster.Status.Phase != string(clusterv1.ClusterPhaseDeleting) {
		log.Debug("delete relation cluster-api obj cluster")
		// delete cluster api obj cluster will delete all relation obj
		if err := c.Delete(ctx, e.Ctx.Cluster); err != nil {
			log.Warn(constant.ReconcileErrorReason, "failed to delete cluster: %v", err)
			return errors.Errorf("failed to delete cluster: %v", err)
		}
		// return now, Wait for the deletion of cluster api obj to trigger the deletion of bkeCluster
		return errors.New("wait for the deletion of cluster api obj to trigger the deletion of bkeCluster")
	}
	return nil
}

// handleBKEMachineDeletion 处理BKEMachine删除
func (e *EnsureDeleteOrReset) handleBKEMachineDeletion(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger) error {
	// need wait all bkeMachine delete
	log.Debug("start delete bkeMachine process")
	bkeMachines := &bkev1beta1.BKEMachineList{}
	if err := c.List(ctx, bkeMachines, client.InNamespace(bkeCluster.Namespace)); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Warn(constant.ReconcileErrorReason, "failed to list bkeMachine: %v", err)
			return errors.Errorf("failed to list bkeMachine: %v", err)
		}
		return errors.Errorf("failed to list bkeMachine: %v", err)
	}
	if len(bkeMachines.Items) > 0 {
		for _, bkeMachine := range bkeMachines.Items {
			// not bootstrapped, cluster API will not delete it
			// manually deleted
			if !bkeMachine.Status.Bootstrapped {
				if err := c.Delete(ctx, &bkeMachine); err != nil {
					if apierrors.IsNotFound(err) {
						continue
					}
					log.Warn(constant.ReconcileErrorReason, "failed to delete bkeMachine: %v", err)
					return err
				}
			}
			// if not have owner, bkemachine Controller will not delete it
			if len(bkeMachine.OwnerReferences) == 0 || bkeMachine.OwnerReferences == nil {
				// try force remove finalizer
				controllerutil.RemoveFinalizer(&bkeMachine, bkev1beta1.BKEMachineFinalizer)
				if err := c.Update(ctx, &bkeMachine); err != nil {
					log.Warn(constant.ReconcileErrorReason, "failed to remove finalizer: %v", err)
					return err
				}
			}
		}
		return errors.New("wait for bkeMachine delete")
	}
	log.Debug("all bkeMachine deleted")
	return nil
}

// deleteRelatedResources 删除相关资源
func (e *EnsureDeleteOrReset) deleteRelatedResources(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger) error {
	// remove all secret type == bke.bocloud.com/secret
	log.Debug("start delete related secret resource")
	if err := c.DeleteAllOf(ctx, &corev1.Secret{}, client.InNamespace(bkeCluster.Namespace), client.MatchingFields{"type": string(agentutils.BKESecretType)}); err != nil {
		log.Warn(constant.ReconcileErrorReason, "failed to delete secret: %v", err)
	}

	// remove all agent Command
	log.Debug("start delete related agent command resource")
	commandList := &agentv1beta1.CommandList{}
	if err := c.List(ctx, commandList, client.InNamespace(bkeCluster.Namespace)); err != nil {
		log.Warn(constant.ReconcileErrorReason, "failed to list command: %v", err)
	} else {
		for _, command := range commandList.Items {
			helper, err := patch.NewHelper(&command, c)
			if err != nil {
				return err
			}
			// remove finalizer and delete
			controllerutil.RemoveFinalizer(&command, "command.bkeagent.bocloud.com/finalizers")
			if err = helper.Patch(ctx, &command); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				log.Warn(constant.ReconcileErrorReason, "failed to remove finalizer: %v", err)
			}
			if err = c.Delete(ctx, &command); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				log.Warn(constant.ReconcileErrorReason, "failed to delete command: %v", err)
			}
		}
	}
	return nil
}

// cleanupClusterResources 清理集群资源
func (e *EnsureDeleteOrReset) cleanupClusterResources(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger) error {
	// delete all associated BKENode resources
	log.Debug("start delete associated BKENode resources")
	if err := c.DeleteAllOf(ctx, &confv1beta1.BKENode{},
		client.InNamespace(bkeCluster.Namespace),
		client.MatchingLabels{"cluster.x-k8s.io/cluster-name": bkeCluster.Name},
	); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Warn(constant.ReconcileErrorReason, "failed to delete BKENode resources: %v", err)
		}
	}

	log.Debug("remove bkeCluster finalizer")
	controllerutil.RemoveFinalizer(bkeCluster, bkev1beta1.ClusterFinalizer)
	// maybe we not need to do anything
	log.Finish(constant.TargetClusterDeletedReason, "bkeCluster deleted successfully")
	if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
		log.Warn(constant.ReconcileErrorReason, "failed to update bkeCluster Status: %v", err)
		return errors.Errorf("failed to update bkeCluster Status: %v", err)
	}
	// remove all event created by bkeCluster
	log.Debug("remove all event created by bkeCluster")
	eventFilters := []client.DeleteAllOfOption{
		client.MatchingFieldsSelector{Selector: fields.AndSelectors(
			fields.OneTermEqualSelector("involvedObject.name", bkeCluster.Name),
			fields.OneTermEqualSelector("involvedObject.namespace", bkeCluster.Namespace),
		)},
		client.InNamespace(bkeCluster.Namespace),
	}
	if err := c.DeleteAllOf(ctx, &corev1.Event{}, eventFilters...); err != nil {
		log.Warn(constant.ReconcileErrorReason, "failed to delete event: %v", err)
	}
	return nil
}

// handleNamespaceDeletion 处理命名空间删除
func (e *EnsureDeleteOrReset) handleNamespaceDeletion(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger) error {
	// 不存在DeleteIgnoreNamespaceAnnotationKey注解，或者有DeleteIgnoreNamespaceAnnotationKey注解，且值为false不忽略，则删除namespace
	if v, ok := annotation.HasAnnotation(bkeCluster, annotation.DeleteIgnoreNamespaceAnnotationKey); ok && v == "false" {
		// remove ns if not have other bkeCluster
		bkeClusters := &bkev1beta1.BKEClusterList{}
		if err := c.List(ctx, bkeClusters, client.InNamespace(bkeCluster.Namespace)); err != nil {
			log.Warn(constant.ReconcileErrorReason, "failed to list bkeCluster: %v", err)
		}
		// 没有其他bkeCluster了，且有DeleteIgnoreNamespaceAnnotationKey注解，且值为false不忽略，则删除namespace
		if len(bkeClusters.Items) == 0 {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: bkeCluster.Namespace,
				},
			}
			log.Info("DeleteNamespace", "remove namespace %q", ns.Name)
			if err := c.Delete(ctx, ns); err != nil {
				log.Warn(constant.ReconcileErrorReason, "failed to delete namespace: %v", err)
			}
		}
	} else {
		if !ok {
			log.Info("DeleteNamespace", "no %s annotation, ignore delete namespace", annotation.DeleteIgnoreNamespaceAnnotationKey)
		}
		if ok && v == "true" {
			log.Info("DeleteNamespace", "%s = %s, ignore delete namespace", annotation.DeleteIgnoreNamespaceAnnotationKey, v)
		}
	}
	// 清理status cache
	statusmanage.BKEClusterStatusManager.RemoveBKEClusterStatusCache(bkeCluster)

	return nil
}

func ensureDeleteOrResetPostHook(p phaseframe.Phase, err error) error {
	if err == nil {
		// remove bkeCluster metrics
		bkemetrics.MetricRegister.Unregister(utils.ClientObjNS(p.GetPhaseContext().BKECluster))
	}
	return nil
}

func (e *EnsureDeleteOrReset) ShutDownAgent(ctx context.Context) {
	_, c, bkeCluster, scheme, log := e.Ctx.Untie()

	needShutDownNodes := bkenode.Nodes{}
	nodeFetcher := e.Ctx.NodeFetcher()
	allNodes, _ := nodeFetcher.GetNodesForBKECluster(ctx, bkeCluster)
	for _, node := range allNodes {
		nodeState, _ := nodeFetcher.GetNodeStateFlagForCluster(ctx, bkeCluster, node.IP, bkev1beta1.NodeAgentPushedFlag)
		if nodeState {
			needShutDownNodes = append(needShutDownNodes, node)
		}
	}

	// 使用公共函数关闭代理
	params := ShutdownAgentOnNodesParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Scheme:     scheme,
		Nodes:      needShutDownNodes,
		Log:        log,
	}
	ShutdownAgentOnNodesWithParams(params)
}

// ShutdownAgentOnNodes 在指定节点上关闭代理
// ShutdownAgentOnNodes 在指定节点上关闭代理
func ShutdownAgentOnNodes(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, scheme *runtime.Scheme, nodes bkenode.Nodes, log *bkev1beta1.BKELogger) {
	params := ShutdownAgentOnNodesParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Scheme:     scheme,
		Nodes:      nodes,
		Log:        log,
	}
	ShutdownAgentOnNodesWithParams(params)
}

// ShutdownAgentOnNodesWithParams 使用参数结构体在多个节点上关闭代理
func ShutdownAgentOnNodesWithParams(params ShutdownAgentOnNodesParams) {
	if params.Nodes.Length() == 0 {
		return
	}
	params.Log.Debug("shutdown agent")

	// 关闭agent
	shutdownAgentCommand := command.Custom{
		BaseCommand: command.BaseCommand{
			Ctx:             params.Ctx,
			NameSpace:       params.BKECluster.Namespace,
			Client:          params.Client,
			Scheme:          params.Scheme,
			OwnerObj:        params.BKECluster,
			ClusterName:     params.BKECluster.Name,
			Unique:          true,
			RemoveAfterWait: true,
			ForceRemove:     true,
			WaitTimeout:     ShutdownAgentWaitTimeoutSeconds * time.Second,
		},
		Nodes:        params.Nodes,
		CommandName:  "shutdown-bkeagent",
		CommandLabel: command.BKEClusterLabel,
	}
	shutdownAgentCommand.CommandSpec = createShutdownAgentCommandSpec()
	_ = shutdownAgentCommand.New()
	_, _, _ = shutdownAgentCommand.Wait()
}

// createShutdownAgentCommandSpec 创建关闭代理的命令规范
func createShutdownAgentCommandSpec() *agentv1beta1.CommandSpec {
	commandSpec := command.GenerateDefaultCommandSpec()
	commandSpec.Commands = []agentv1beta1.ExecCommand{
		{
			ID: "Shutdown agent",
			Command: []string{
				"Shutdown",
			},
			Type:          agentv1beta1.CommandBuiltIn,
			BackoffIgnore: false,
		},
	}
	return commandSpec
}

// ShutdownAgentOnNodesParams 包含在多个节点上关闭代理的参数
type ShutdownAgentOnNodesParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Scheme     *runtime.Scheme
	Nodes      bkenode.Nodes
	Log        *bkev1beta1.BKELogger
}

// ShutdownAgentOnSingleNodeParams 包含在单个节点上关闭代理的参数
type ShutdownAgentOnSingleNodeParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Scheme     *runtime.Scheme
	Node       confv1beta1.Node
	Log        *zap.SugaredLogger
}

// ShutdownAgentOnSingleNode 在单个节点上关闭代理
func ShutdownAgentOnSingleNode(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, scheme *runtime.Scheme, node confv1beta1.Node, log *bkev1beta1.BKELogger) error {
	params := ShutdownAgentOnSingleNodeParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Scheme:     scheme,
		Node:       node,
		Log:        log.NormalLogger,
	}
	return ShutdownAgentOnSingleNodeWithParams(params)
}

// ShutdownAgentOnSingleNodeWithParams 使用参数结构体在单个节点上关闭代理
func ShutdownAgentOnSingleNodeWithParams(params ShutdownAgentOnSingleNodeParams) error {
	var nodes bkenode.Nodes
	nodes = append(nodes, params.Node)
	params.Log.Debug("shutdown agent")

	// 关闭agent
	shutdownAgentCommand := command.Custom{
		BaseCommand: command.BaseCommand{
			Ctx:             params.Ctx,
			NameSpace:       params.BKECluster.Namespace,
			Client:          params.Client,
			Scheme:          params.Scheme,
			OwnerObj:        params.BKECluster,
			ClusterName:     params.BKECluster.Name,
			Unique:          true,
			RemoveAfterWait: true,
			ForceRemove:     true,
			WaitTimeout:     ShutdownAgentWaitTimeoutSeconds * time.Second,
		},
		Nodes:        nodes,
		CommandName:  fmt.Sprintf("shutdown-bkeagent-%s", params.Node.IP),
		CommandLabel: command.BKEClusterLabel,
	}
	shutdownAgentCommand.CommandSpec = createShutdownAgentCommandSpec()
	if err := shutdownAgentCommand.New(); err != nil {
		return err
	}
	_, _, _ = shutdownAgentCommand.Wait()
	return nil
}

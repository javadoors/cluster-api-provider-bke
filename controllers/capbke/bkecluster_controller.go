/******************************************************************
 * Copyright (c) 2024 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package capbke

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/remote"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	bkemetrics "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/metrics"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phases"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/statusmanage"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clustertracker"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clusterutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/condition"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/config"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	l "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
	bkepredicates "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/predicates"
)

const (
	nodeWatchRequeueInterval = 10 * time.Second
)

// BKEClusterReconciler reconciles a BKECluster object
type BKEClusterReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	RestConfig  *rest.Config
	Tracker     *remote.ClusterCacheTracker
	controller  controller.Controller
	NodeFetcher *nodeutil.NodeFetcher
}

// initNodeFetcher initializes the NodeFetcher if not already set
func (r *BKEClusterReconciler) initNodeFetcher() {
	if r.NodeFetcher == nil {
		r.NodeFetcher = nodeutil.NewNodeFetcher(r.Client)
	}
}

// +kubebuilder:rbac:groups=bke.bocloud.com,resources=*,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.x-k8s.io;controlplane.cluster.x-k8s.io;bootstrap.cluster.x-k8s.io,resources=*,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events;secrets;configmaps;namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=bkeagent.bocloud.com,resources=commands,verbs=get;list;watch;create;update;patch;delete
// Reconcile is the main logic of bke cluster controller.
func (r *BKEClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// 获取并验证集群资源
	bkeCluster, err := r.getAndValidateCluster(ctx, req)
	if err != nil {
		return r.handleClusterError(err)
	}

	// 处理指标注册
	r.registerMetrics(bkeCluster)

	// 获取旧版本集群配置
	oldBkeCluster, err := r.getOldBKECluster(bkeCluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 初始化日志记录器
	bkeLogger := r.initializeLogger(bkeCluster)

	// 处理代理和节点状态
	if err = r.handleClusterStatus(ctx, bkeCluster, bkeLogger); err != nil {
		return ctrl.Result{}, err
	}

	// 初始化阶段上下文并执行阶段流程
	phaseResult, err := r.executePhaseFlow(ctx, bkeCluster, oldBkeCluster, bkeLogger)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 设置集群监控
	watchResult, err := r.setupClusterWatching(ctx, bkeCluster, bkeLogger)
	if err != nil {
		return watchResult, err
	}

	// 返回最终结果
	result, err := r.getFinalResult(phaseResult, bkeCluster)
	return result, err
}

// getAndValidateCluster 获取并验证集群资源
func (r *BKEClusterReconciler) getAndValidateCluster(
	ctx context.Context,
	req ctrl.Request) (*bkev1beta1.BKECluster, error) {
	bkeCluster, err := mergecluster.GetCombinedBKECluster(ctx, r.Client, req.Namespace, req.Name)
	if err != nil {
		return nil, err
	}
	return bkeCluster, nil
}

// handleClusterError 处理集群错误
func (r *BKEClusterReconciler) handleClusterError(err error) (ctrl.Result, error) {
	if apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, err
}

// registerMetrics 处理指标注册
func (r *BKEClusterReconciler) registerMetrics(bkeCluster *bkev1beta1.BKECluster) {
	if config.MetricsAddr != "0" {
		bkemetrics.MetricRegister.Register(utils.ClientObjNS(bkeCluster))
	}
}

// getOldBKECluster 获取旧版本集群配置
func (r *BKEClusterReconciler) getOldBKECluster(bkeCluster *bkev1beta1.BKECluster) (*bkev1beta1.BKECluster, error) {
	return mergecluster.GetLastUpdatedBKECluster(bkeCluster)
}

// initializeLogger 初始化日志记录器
func (r *BKEClusterReconciler) initializeLogger(bkeCluster *bkev1beta1.BKECluster) *bkev1beta1.BKELogger {
	log := l.Named("Reconcile").With("bkeCluster", bkeCluster.Name,
		"namespace", bkeCluster.Namespace)
	return bkev1beta1.NewBKELogger(log, r.Recorder, bkeCluster)
}

// handleClusterStatus 处理代理和节点状态
func (r *BKEClusterReconciler) handleClusterStatus(ctx context.Context, bkeCluster *bkev1beta1.BKECluster,
	bkeLogger *bkev1beta1.BKELogger) error {
	if err := r.computeAgentStatus(ctx, bkeCluster); err != nil {
		bkeLogger.Error(constant.InternalErrorReason, "failed set AgentStatus, err: %v", err)
		return err
	}

	if err := r.initNodeStatus(ctx, bkeCluster); err != nil {
		bkeLogger.Error(constant.InternalErrorReason, "failed set NodeStatus, err: %v", err)
		return err
	}
	return nil
}

// executePhaseFlow 初始化阶段上下文并执行阶段流程
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

// setupClusterWatching 设置集群监控
func (r *BKEClusterReconciler) setupClusterWatching(ctx context.Context,
	bkeCluster *bkev1beta1.BKECluster,
	bkeLogger *bkev1beta1.BKELogger) (ctrl.Result, error) {
	if clustertracker.AllowTrackerRemoteCluster(bkeCluster) {
		// 监听集群节点状态，如果有节点状态变更，触发集群健康检查
		watchInput := remote.WatchInput{
			Name:         utils.ClientObjNS(bkeCluster),
			Cluster:      util.ObjectKey(bkeCluster),
			Watcher:      r.controller,
			Kind:         &corev1.Node{},
			EventHandler: handler.EnqueueRequestsFromMapFunc(nodeToBKEClusterMapFunc(ctx, r.Client)),
			Predicates:   []predicate.Predicate{bkepredicates.NodeNotReadyPredicate()},
		}

		if err := r.Tracker.Watch(ctx, watchInput); err != nil {
			bkeLogger.Error(constant.ClusterTracker, "failed to watch node, err: %v", err)
			return ctrl.Result{RequeueAfter: nodeWatchRequeueInterval}, nil
		}
	}
	return ctrl.Result{}, nil
}

// getFinalResult 返回最终结果
func (r *BKEClusterReconciler) getFinalResult(phaseResult ctrl.Result,
	bkeCluster *bkev1beta1.BKECluster) (ctrl.Result, error) {
	// if need requeue, return
	if phaseResult.Requeue || phaseResult.RequeueAfter > 0 {
		return phaseResult, nil
	}

	return statusmanage.BKEClusterStatusManager.GetCtrlResult(bkeCluster), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BKEClusterReconciler) SetupWithManager(ctx context.Context,
	mgr ctrl.Manager,
	options controller.Options) error {
	// Initialize NodeFetcher
	r.NodeFetcher = nodeutil.NewNodeFetcher(mgr.GetClient())

	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&bkev1beta1.BKECluster{},
			builder.WithPredicates(predicate.Or(
				bkepredicates.BKEClusterAnnotationsChange(),
				bkepredicates.BKEClusterSpecChange(),
			)),
		).
		WithOptions(options).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(clusterToBKEClusterMapFunc(ctx,
				bkev1beta1.GroupVersion.WithKind("BKECluster"),
				mgr.GetClient(), &bkev1beta1.BKECluster{})),
			builder.WithPredicates(bkepredicates.ClusterUnPause()),
		).
		// Watch BKENode resources and trigger reconcile for associated BKECluster
		Watches(
			&confv1beta1.BKENode{},
			handler.EnqueueRequestsFromMapFunc(r.bkeNodeToBKEClusterMapFunc()),
			builder.WithPredicates(bkepredicates.BKENodeChange()),
		).Build(r)
	if err != nil {
		return errors.Errorf("failed setting up with a controller manager: %v", err)
	}
	r.controller = c
	return nil
}

// bkeNodeToBKEClusterMapFunc returns a handler.MapFunc that maps BKENode events to BKECluster reconcile requests
func (r *BKEClusterReconciler) bkeNodeToBKEClusterMapFunc() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		bkeNode, ok := obj.(*confv1beta1.BKENode)
		if !ok {
			return nil
		}

		// Get cluster name from label
		clusterName := bkeNode.Labels[nodeutil.ClusterNameLabel]
		if clusterName == "" {
			return nil
		}

		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{
				Name:      clusterName,
				Namespace: bkeNode.Namespace,
			},
		}}
	}
}

func clusterToBKEClusterMapFunc(ctx context.Context,
	gvk schema.GroupVersionKind,
	c client.Client, providerCluster client.Object) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		cluster, ok := o.(*clusterv1.Cluster)
		if !ok {
			return nil
		}

		// Return early if the Cluster DeletionTimestamp != 0.
		if !cluster.DeletionTimestamp.IsZero() {
			return nil
		}

		// Return early if the InfrastructureRef is nil.
		if cluster.Spec.InfrastructureRef == nil {
			return nil
		}
		gk := gvk.GroupKind()
		// Return early if the GroupKind doesn't match what we expect.
		infraGK := cluster.Spec.InfrastructureRef.GroupVersionKind().GroupKind()
		if gk != infraGK {
			return nil
		}
		providerCluster, ok := providerCluster.DeepCopyObject().(client.Object)
		if !ok {
			l.Errorf("Failed to cast providerCluster to client.Object")
			return nil
		}
		key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Spec.InfrastructureRef.Name}

		if err := c.Get(ctx, key, providerCluster); err != nil {
			l.Errorf("Failed to get %T err: %v", providerCluster, err)
			return nil
		}

		if annotations.IsExternallyManaged(providerCluster) {
			l.Errorf("%T is externally managed, skipping mapping", providerCluster)
			return nil
		}

		return []reconcile.Request{
			{
				NamespacedName: client.ObjectKey{
					Namespace: cluster.Namespace,
					Name:      cluster.Spec.InfrastructureRef.Name,
				},
			},
		}
	}
}

func nodeToBKEClusterMapFunc(ctx context.Context, c client.Client) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		node, ok := o.(*corev1.Node)
		if !ok {
			return nil
		}

		clusterName, ok := annotation.HasAnnotation(node, clusterv1.ClusterNameAnnotation)
		if !ok {
			return nil
		}
		clusterNamespace, ok := annotation.HasAnnotation(node, clusterv1.ClusterNamespaceAnnotation)
		if !ok {
			return nil
		}
		cluster := &clusterv1.Cluster{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: clusterNamespace, Name: clusterName},
			cluster); err != nil {
			l.Errorf("Failed to get Cluster %s/%s err: %v", clusterNamespace, clusterName, err)
			return nil
		}

		if cluster.Spec.InfrastructureRef == nil {
			return nil
		}

		return []reconcile.Request{
			{
				NamespacedName: client.ObjectKey{
					Namespace: cluster.Spec.InfrastructureRef.Namespace,
					Name:      cluster.Spec.InfrastructureRef.Name,
				},
			},
		}
	}
}

func (r *BKEClusterReconciler) computeAgentStatus(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) error {
	statusCopy := bkeCluster.Status.AgentStatus.DeepCopy()

	// 从 BKENode CRD 获取节点数量
	nodeCount, err := r.NodeFetcher.GetNodeCountForCluster(ctx, bkeCluster)
	if err != nil {
		return err
	}

	bkeCluster.Status.AgentStatus.Replies = int32(nodeCount)
	// 初始化agentStatus
	if bkeCluster.Status.AgentStatus.Status == "" {
		bkeCluster.Status.AgentStatus.UnavailableReplies = int32(nodeCount)
		bkeCluster.Status.AgentStatus.Status = fmt.Sprintf("%d/%d", 0, nodeCount)
	} else {
		availableNodesNum := 0
		status := strings.Split(statusCopy.Status, "/")
		if v, err := strconv.Atoi(status[0]); err == nil {
			availableNodesNum = v
		}
		if availableNodesNum > nodeCount {
			availableNodesNum = nodeCount
		}
		bkeCluster.Status.AgentStatus.UnavailableReplies = int32(nodeCount - availableNodesNum)
		bkeCluster.Status.AgentStatus.Status = fmt.Sprintf("%d/%d", availableNodesNum, nodeCount)
		bkeCluster.Status.AgentStatus.Replies = int32(nodeCount)
	}
	if !statusCopy.Equal(&bkeCluster.Status.AgentStatus) {
		if err := mergecluster.SyncStatusUntilComplete(r.Client, bkeCluster); err != nil {
			return err
		}
	}
	return nil
}

func (r *BKEClusterReconciler) initNodeStatus(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) error {
	// 处理节点变化 - now uses ctx to fetch BKENodes
	nodeChangeFlag := r.handleNodeChanges(ctx, bkeCluster)

	// 获取各种状态标志
	deployFlag, upgradeFlag, manageFlag := r.getNodeFlags(ctx, bkeCluster)
	deployFailedFlag, upgradeFailedFlag, manageFailedFlag := r.getClusterStatusFlags(bkeCluster)

	// 处理重试逻辑
	retryFlag, patchFunc := r.handleRetryLogic(ctx, bkeCluster)

	// 设置集群健康状态
	flags := ClusterHealthStatusFlags{
		DeployFlag:        deployFlag,
		UpgradeFlag:       upgradeFlag,
		ManageFlag:        manageFlag,
		DeployFailedFlag:  deployFailedFlag,
		UpgradeFailedFlag: upgradeFailedFlag,
		ManageFailedFlag:  manageFailedFlag,
	}
	r.setClusterHealthStatus(bkeCluster, flags)

	// 同步状态
	params := SyncNodeStatusParams{
		DeployFlag:        deployFlag,
		DeployFailedFlag:  deployFailedFlag,
		UpgradeFlag:       upgradeFlag,
		UpgradeFailedFlag: upgradeFailedFlag,
		ManageFailedFlag:  manageFailedFlag,
		RetryFlag:         retryFlag,
		NodeChangeFlag:    nodeChangeFlag,
		PatchFunc:         patchFunc,
	}
	return r.syncNodeStatusIfNeeded(bkeCluster, params)
}

// handleNodeChanges 处理节点变化
func (r *BKEClusterReconciler) handleNodeChanges(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) bool {
	// Fetch nodes from BKENode CRD
	bkeNodes, err := r.NodeFetcher.GetNodesForBKECluster(ctx, bkeCluster)
	if err != nil {
		log.Warnf("Failed to fetch BKENodes for cluster %s: %v", bkeCluster.Name, err)
		return false
	}

	// Get current node states from BKENode CRD
	nodeStates, err := r.NodeFetcher.GetNodeStatesForBKECluster(ctx, bkeCluster)
	if err != nil {
		log.Warnf("Failed to get node states for cluster %s: %v", bkeCluster.Name, err)
		return false
	}

	// Convert nodeStates to Nodes for comparison
	var statusNodes bkenode.Nodes
	for _, ns := range nodeStates {
		statusNodes = append(statusNodes, ns.Node)
	}

	nodeT, nodeChangeFlag := bkenode.CompareBKEConfigNode(statusNodes, bkeNodes)
	if nodeChangeFlag {
		// Process node transitions by updating BKENode CRD status
		for _, t := range nodeT {
			switch t.Operate {
			case bkenode.CreateNode:
				// New node - BKENode should already exist, just log
				log.Debugf("新增节点 %s", phaseutil.NodeInfo(*t.Node))
			case bkenode.RemoveNode:
				// Mark node for deletion
				if err := r.NodeFetcher.UpdateBKENodeState(ctx, bkeCluster.Namespace, bkeCluster.Name,
					t.Node.IP, confv1beta1.NodeDeleting, "Node marked for deletion"); err != nil {
					log.Warnf("Failed to update node state for deletion: %v", err)
				}
				log.Debugf("准备删除节点 %s", phaseutil.NodeInfo(*t.Node))
			case bkenode.UpdateNode:
				log.Debugf("更新节点 %s", phaseutil.NodeInfo(*t.Node))
			default:
				log.Debugf("未知的节点操作类型: %v", t.Operate)
			}
		}
	}

	return nodeChangeFlag
}

// 获取节点状态相关标志
func (r *BKEClusterReconciler) getNodeFlags(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) (bool, bool, bool) {
	// Get node count from BKENode CRD
	nodeCount, err := r.NodeFetcher.GetNodeCountForCluster(ctx, bkeCluster)
	if err != nil {
		log.Warnf("Failed to get node count: %v", err)
		nodeCount = 0
	}
	// 是否是初次部署
	deployFlag := nodeCount == 0

	// 是否需要升级集群
	bkeNodes, err := r.NodeFetcher.GetBKENodesWrapperForCluster(ctx, bkeCluster)
	if err != nil {
		log.Warnf("Failed to get BKENodes for upgrade check: %v", err)
		bkeNodes = nil
	}
	upgradeFlag := phaseutil.GetNeedUpgradeNodesWithBKENodes(bkeCluster, bkeNodes).Length() > 0
	// 是否需要纳管集群
	manageFlag := clusterutil.IsBocloudCluster(bkeCluster) && !clusterutil.FullyControlled(bkeCluster)

	return deployFlag, upgradeFlag, manageFlag
}

// 获取集群状态标志
func (r *BKEClusterReconciler) getClusterStatusFlags(bkeCluster *bkev1beta1.BKECluster) (bool, bool, bool) {
	deployFailedFlag := false
	upgradeFailedFlag := false
	manageFailedFlag := false
	// 获取当前集群最终状态
	v, ok := condition.HasCondition(bkev1beta1.ClusterHealthyStateCondition, bkeCluster)
	if ok && v != nil {
		deployFailedFlag = v.Reason == string(bkev1beta1.Deploying) && v.Message == string(bkev1beta1.DeployFailed)
		upgradeFailedFlag = v.Reason == string(bkev1beta1.Upgrading) && v.Message == string(bkev1beta1.UpgradeFailed)
		manageFailedFlag = v.Reason == string(bkev1beta1.Managing) && v.Message == string(bkev1beta1.ManageFailed)
	}

	return deployFailedFlag, upgradeFailedFlag, manageFailedFlag
}

// 处理重试逻辑
func (r *BKEClusterReconciler) handleRetryLogic(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) (bool, func(*bkev1beta1.BKECluster)) {
	retryFlag := false
	patchFunc := func(cluster *bkev1beta1.BKECluster) { return }

	// 检查是否存在重试注解
	if retryNodeIPs, ok := annotation.HasAnnotation(bkeCluster, annotation.RetryAnnotationKey); ok {
		// 处理重试逻辑
		r.processRetryLogic(ctx, bkeCluster, retryNodeIPs)
		retryFlag = true
		// 准备清理重试注解的函数
		patchFunc = r.createRemoveRetryAnnotationFunc()
	}

	return retryFlag, patchFunc
}

// processRetryLogic 处理重试逻辑
func (r *BKEClusterReconciler) processRetryLogic(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, retryNodeIPs string) {
	if retryNodeIPs == "" {
		r.processAllNodesRetry(ctx, bkeCluster)
	} else {
		r.processSpecificNodesRetry(ctx, bkeCluster, retryNodeIPs)
	}
}

// processAllNodesRetry 处理所有节点的重试逻辑
func (r *BKEClusterReconciler) processAllNodesRetry(ctx context.Context, bkeCluster *bkev1beta1.BKECluster) {
	nodeStates, err := r.NodeFetcher.GetNodeStatesForBKECluster(ctx, bkeCluster)
	if err != nil {
		log.Warnf("Failed to get node states for cluster %s: %v", bkeCluster.Name, err)
		return
	}

	log.Debugf("重试标记存在，清理所有节点的失败状态码")
	// 清理所有节点的失败状态
	for _, nodeState := range nodeStates {
		hasFailedFlag, err := r.NodeFetcher.GetNodeStateFlagForCluster(ctx, bkeCluster, nodeState.Node.IP, bkev1beta1.NodeFailedFlag)
		if err != nil {
			log.Warnf("Failed to get node state flag for node %s: %v", nodeState.Node.IP, err)
			continue
		}
		if hasFailedFlag {
			if err := r.NodeFetcher.UnmarkNodeStateFlagForCluster(ctx, bkeCluster, nodeState.Node.IP, bkev1beta1.NodeFailedFlag); err != nil {
				log.Warnf("Failed to unmark node state flag for node %s: %v", nodeState.Node.IP, err)
			}
		}
	}
	// 重置状态管理器
	log.Debugf("重置状态管理器")
	statusmanage.BKEClusterStatusManager.RemoveClusterStatusManagerCache(bkeCluster)
}

// processSpecificNodesRetry 处理指定节点的重试逻辑
func (r *BKEClusterReconciler) processSpecificNodesRetry(ctx context.Context, bkeCluster *bkev1beta1.BKECluster, retryNodeIPs string) {
	retryNodes := strings.Split(retryNodeIPs, ",")
	// 清理指定节点的失败状态
	for _, nodeIP := range retryNodes {
		log.Debugf("重试标记存在，清理节点 %s 的失败状态码", nodeIP)
		hasFailedFlag, err := r.NodeFetcher.GetNodeStateFlagForCluster(ctx, bkeCluster, nodeIP, bkev1beta1.NodeFailedFlag)
		if err != nil {
			log.Warnf("Failed to get node state flag for node %s: %v", nodeIP, err)
			continue
		}
		if hasFailedFlag {
			if err := r.NodeFetcher.UnmarkNodeStateFlagForCluster(ctx, bkeCluster, nodeIP, bkev1beta1.NodeFailedFlag); err != nil {
				log.Warnf("Failed to unmark node state flag for node %s: %v", nodeIP, err)
			}
		}
		log.Debugf("重试标记存在，移除节点 %s 的状态缓存", nodeIP)
		statusmanage.BKEClusterStatusManager.RemoveSingleNodeStatusCache(bkeCluster, nodeIP)
	}
}

// createRemoveRetryAnnotationFunc 创建移除重试注解的函数
func (r *BKEClusterReconciler) createRemoveRetryAnnotationFunc() func(*bkev1beta1.BKECluster) {
	return func(cluster *bkev1beta1.BKECluster) {
		// 移除retry annotation
		annotation.RemoveAnnotation(cluster, annotation.RetryAnnotationKey)
	}
}

// ClusterHealthStatusFlags 包含集群健康状态相关的标志
type ClusterHealthStatusFlags struct {
	DeployFlag        bool
	UpgradeFlag       bool
	ManageFlag        bool
	DeployFailedFlag  bool
	UpgradeFailedFlag bool
	ManageFailedFlag  bool
}

// 设置集群健康状态
func (r *BKEClusterReconciler) setClusterHealthStatus(bkeCluster *bkev1beta1.BKECluster, flags ClusterHealthStatusFlags) {
	// 首次部署设置为正在部署
	if flags.DeployFlag || flags.DeployFailedFlag {
		markBKEClusterHealthyStatus(bkeCluster, bkev1beta1.Deploying)
	}
	// 需要升级集群设置为正在升级
	if flags.UpgradeFlag || flags.UpgradeFailedFlag {
		markBKEClusterHealthyStatus(bkeCluster, bkev1beta1.Upgrading)
	}
	// 需要纳管集群设置为正在纳管
	if flags.ManageFlag || flags.ManageFailedFlag {
		markBKEClusterHealthyStatus(bkeCluster, bkev1beta1.Managing)
	}

	// 删除集群
	if phaseutil.IsDeleteOrReset(bkeCluster) {
		markBKEClusterHealthyStatus(bkeCluster, bkev1beta1.Deleting)
	}
}

// SyncNodeStatusParams 包含同步节点状态所需的参数
type SyncNodeStatusParams struct {
	DeployFlag        bool
	DeployFailedFlag  bool
	UpgradeFlag       bool
	UpgradeFailedFlag bool
	ManageFailedFlag  bool
	RetryFlag         bool
	NodeChangeFlag    bool
	PatchFunc         func(*bkev1beta1.BKECluster)
}

// 根据条件同步节点状态
func (r *BKEClusterReconciler) syncNodeStatusIfNeeded(bkeCluster *bkev1beta1.BKECluster,
	params SyncNodeStatusParams) error {
	managementAndOtherTriggers := params.ManageFailedFlag || params.RetryFlag || params.NodeChangeFlag
	deploymentRelated := params.DeployFlag || params.DeployFailedFlag
	upgradeRelated := params.UpgradeFlag || params.UpgradeFailedFlag

	if deploymentRelated || upgradeRelated || managementAndOtherTriggers {
		if err := mergecluster.SyncStatusUntilComplete(r.Client, bkeCluster, params.PatchFunc); err != nil {
			return err
		}
	}

	return nil
}

func markBKEClusterHealthyStatus(bkeCluster *bkev1beta1.BKECluster, status confv1beta1.ClusterHealthState) {
	log.Debugf("标记集群 %s 状态为 %s", utils.ClientObjNS(bkeCluster), status)
	bkeCluster.Status.ClusterHealthState = status
	condition.ConditionMark(bkeCluster, bkev1beta1.ClusterHealthyStateCondition,
		confv1beta1.ConditionTrue, string(status), "")
}

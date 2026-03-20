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

package phases

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clusterutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	EnsureWorkerJoinName confv1beta1.BKEClusterPhase = "EnsureWorkerJoin"
)

type EnsureWorkerJoin struct {
	phaseframe.BasePhase
	nodesToJoin bkenode.Nodes
}

func NewEnsureWorkerJoin(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureWorkerJoinName)
	return &EnsureWorkerJoin{BasePhase: base}
}

func (e *EnsureWorkerJoin) Execute() (ctrl.Result, error) {
	if err := e.reconcileWorkerJoin(); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (e *EnsureWorkerJoin) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}

	bkeNodes, ok := fetchBKENodesIfCPInitialized(e.Ctx, new)
	if !ok {
		return false
	}
	nodes := phaseutil.GetNeedJoinWorkerNodesWithBKENodes(new, bkeNodes)
	if nodes.Length() == 0 {
		return false
	}

	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

func (e *EnsureWorkerJoin) getExceptJoinNodes() bkenode.Nodes {
	_, _, bkeCluster, _, log := e.Ctx.Untie()
	// Use NodeFetcher to get BKENodes from API server
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
	if err != nil {
		log.Warn(constant.WorkerNodeSkipReason, "failed to get BKENodes: %v", err)
		return nil
	}
	nodes := phaseutil.GetNeedJoinWorkerNodesWithBKENodes(bkeCluster, bkeNodes)
	exceptJoinNodes := bkenode.Nodes{}
	nodeFetcher := e.Ctx.NodeFetcher()
	for _, node := range nodes {
		// 检查是否需要跳过该节点
		needSkip, _ := nodeFetcher.GetNodeStateNeedSkip(e.Ctx, bkeCluster.Namespace, bkeCluster.Name, node.IP)
		if needSkip {
			log.Info(constant.WorkerNodeSkipReason, "Node is marked as skip, skip join node. node: %v", phaseutil.NodeInfo(node))
			continue
		}
		envFlag, _ := nodeFetcher.GetNodeStateFlagForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeEnvFlag)
		readyFlag, _ := nodeFetcher.GetNodeStateFlagForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeAgentReadyFlag)
		if !envFlag || !readyFlag {
			continue
		}
		exceptJoinNodes = append(exceptJoinNodes, node)
	}
	return exceptJoinNodes
}

func (e *EnsureWorkerJoin) reconcileWorkerJoin() error {
	ctx, c, bkeCluster, _, log := e.Ctx.Untie()

	// 检查控制平面是否已初始化
	if conditions.IsFalse(e.Ctx.Cluster, clusterv1.ControlPlaneInitializedCondition) {
		log.Warn(constant.MasterNotInitReason, "master is not initialized, skip join worker nodes process")
		return nil
	}

	// 获取需要加入的节点
	exceptJoinNodes := e.getExceptJoinNodes()
	if exceptJoinNodes.Length() == 0 {
		return nil
	}

	// 获取可加入的节点信息
	nodesInfos, nodesCount, err := e.getJoinableNodesInfo(exceptJoinNodes)
	if err != nil {
		return err
	}

	// 处理博云集群的特殊配置
	bocloudParams := HandleBocloudClusterConfigParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Log:        log,
	}
	if err = e.handleBocloudClusterConfig(bocloudParams); err != nil {
		return err
	}

	log.Info(constant.WorkerJoiningReason, "%d nodes will be joined, nodes: %v", nodesCount, strings.Join(nodesInfos, ", "))

	// 获取集群API关联对象
	scope, err := phaseutil.GetClusterAPIAssociateObjs(ctx, c, e.Ctx.Cluster)
	if err != nil || scope.MachineDeployment == nil {
		log.Error(constant.WorkerJoinFailedReason, "Get cluster-api associate objs failed. err: %v", err)
		return err
	}

	// 调整MachineDeployment副本数
	scaleParams := ScaleMachineDeploymentParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Scope:      scope,
		NodesCount: nodesCount,
	}
	return e.scaleMachineDeployment(scaleParams)
}

// HandleBocloudClusterConfigParams 包含处理博云集群特殊配置所需的参数
type HandleBocloudClusterConfigParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Log        *bkev1beta1.BKELogger
}

// handleBocloudClusterConfig 处理博云集群的特殊配置
func (e *EnsureWorkerJoin) handleBocloudClusterConfig(params HandleBocloudClusterConfigParams) error {
	if clusterutil.IsBocloudCluster(params.BKECluster) {
		params.Log.Info(constant.WorkerJoiningReason, "bocloud cluster need to distribute kube-proxy kubeconfig")
		if err := phaseutil.DistributeKubeProxyKubeConfig(params.Ctx, params.Client, params.BKECluster, e.nodesToJoin, params.Log); err != nil {
			return err
		}
	}
	return nil
}

// ScaleMachineDeploymentParams 包含调整MachineDeployment副本数所需的参数
type ScaleMachineDeploymentParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Scope      *phaseutil.ClusterAPIObjs
	NodesCount int
}

// scaleMachineDeployment 调整MachineDeployment副本数
func (e *EnsureWorkerJoin) scaleMachineDeployment(params ScaleMachineDeploymentParams) error {
	bkeNodes, _ := e.Ctx.NodeFetcher().GetNodesForBKECluster(params.Ctx, params.BKECluster)
	workerNodes := bkeNodes.Worker()

	specCopy := params.Scope.MachineDeployment.Spec.DeepCopy()
	currentReplicas := specCopy.Replicas

	exceptReplicas := *currentReplicas + int32(params.NodesCount)
	// 无论如何不能超过bkecluster的worker数量
	if exceptReplicas > int32(workerNodes.Length()) {
		exceptReplicas = int32(workerNodes.Length())
	}

	params.Scope.MachineDeployment.Spec.Replicas = &exceptReplicas

	_, _, _, _, log := e.Ctx.Untie()
	log.Info(constant.WorkerJoiningReason, "Scale up MachineDeployment replicas %d to %d", *currentReplicas, exceptReplicas)

	// 如果节点加入过程中出现异常，需要将节点数量恢复到加入前的状态
	var scaleErr error
	defer func() {
		if scaleErr != nil {
			log.Info(constant.WorkerJoinFailedReason, "Scale down MachineDeployment replicas to %d.", *currentReplicas)
			params.Scope.MachineDeployment.Spec.Replicas = currentReplicas
			if err := phaseutil.ResumeClusterAPIObj(params.Ctx, params.Client, params.Scope.MachineDeployment); err != nil {
				log.Error(constant.WorkerJoinFailedReason, "Rollback MachineDeployment replicas failed. err: %v", err)
			}
		}
	}()

	if scaleErr = phaseutil.ResumeClusterAPIObj(params.Ctx, params.Client, params.Scope.MachineDeployment); scaleErr != nil {
		log.Error(constant.WorkerJoinFailedReason, "Scale up MachineDeployment replicas failed. err: %v", scaleErr)
		return scaleErr
	}

	// 等待worker节点加入
	if scaleErr = e.waitWorkerJoin(); scaleErr != nil {
		log.Error(constant.WorkerJoinFailedReason, "Wait for worker join failed. err: %v", scaleErr)
		return scaleErr
	}

	return nil
}

func (e *EnsureWorkerJoin) getJoinableNodesInfo(exceptJoinNodes bkenode.Nodes) ([]string, int, error) {
	ctx, c, bkeCluster, _, log := e.Ctx.Untie()

	var nodesInfos []string
	nodesCount := 0
	for _, node := range exceptJoinNodes {
		// 正常应该是找不到关联的machine的
		if _, err := phaseutil.NodeToMachine(ctx, c, bkeCluster, node); err == nil {
			log.Warn(constant.WorkerJoinedReason, "Node already exists, skip join node. node: %v", node.Hostname)
			e.Ctx.NodeFetcher().MarkNodeStateFlagForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeBootFlag)
		} else {
			nodesInfos = append(nodesInfos, phaseutil.NodeInfo(node))
			nodesCount++
			e.nodesToJoin = append(e.nodesToJoin, node)
		}
	}
	if nodesCount == 0 {
		log.Info(constant.WorkerJoinedReason, "All nodes have been joined, skip join node.")
		if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
			return nil, 0, err
		}
		return nil, 0, nil
	}
	return nodesInfos, nodesCount, nil
}

func (e *EnsureWorkerJoin) waitWorkerJoin() error {
	if len(e.nodesToJoin) == 0 || e.nodesToJoin == nil {
		return nil
	}
	ctx, c, _, _, log := e.Ctx.Untie()

	// 获取超时设置
	timeOut, err := phaseutil.GetBootTimeOut(e.Ctx.BKECluster)
	if err != nil {
		log.Warn(constant.MasterNotInitReason, "Get boot timeout failed. err: %v", err)
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, timeOut)
	defer cancel()

	// 调用轮询函数
	successJoinNode, err := e.pollWorkerJoinStatus(ctxTimeout)
	if err != nil {
		log.Warn("pollWorkerJoinStatus", "pollWorkerJoinStatus failed. err: %v", err)
	}

	// 分类节点（成功和失败）
	successNodes, failedNodes := e.categorizeJoinedNodes(successJoinNode)

	// 更新成功节点的状态
	if err := e.updateSuccessNodesStatus(c, successNodes); err != nil {
		return err
	}

	// 处理失败节点
	if len(failedNodes) > 0 {
		e.handleFailedNodes(c, successNodes, failedNodes)
	}

	// 判断是否可以继续部署
	return e.determineDeploymentResult(successNodes, failedNodes, err)
}

// categorizeJoinedNodes 将节点分类为成功和失败两组
func (e *EnsureWorkerJoin) categorizeJoinedNodes(successJoinNode *sync.Map) (bkenode.Nodes, bkenode.Nodes) {
	failedNodes := bkenode.Nodes{}
	successNodes := bkenode.Nodes{}

	for i, node := range e.nodesToJoin {
		if _, ok := successJoinNode.Load(i); !ok {
			failedNodes = append(failedNodes, node)
		} else {
			successNodes = append(successNodes, node)
		}
	}
	return successNodes, failedNodes
}

// updateSuccessNodesStatus 更新成功节点的状态
func (e *EnsureWorkerJoin) updateSuccessNodesStatus(
	c client.Client, successNodes bkenode.Nodes) error {

	if successNodes.Length() == 0 {
		return nil
	}

	if err := e.Ctx.RefreshCtxBKECluster(); err != nil {
		return err
	}

	for _, node := range successNodes {
		e.Ctx.NodeFetcher().SetNodeStateWithMessageForCluster(e.Ctx, e.Ctx.BKECluster,
			node.IP, bkev1beta1.NodeNotReady, "Join worker nodes success")
	}

	return mergecluster.SyncStatusUntilComplete(c, e.Ctx.BKECluster)
}

// handleFailedNodes 处理失败的节点
func (e *EnsureWorkerJoin) handleFailedNodes(
	c client.Client, successNodes, failedNodes bkenode.Nodes) {

	_, _, _, _, log := e.Ctx.Untie()

	e.logFailedNodesSummary(log, successNodes, failedNodes)
	e.markFailedNodesAsSkipped(log, failedNodes)
	e.logFailedNodesGuidance(log)

	// 同步失败节点的状态
	if err := mergecluster.SyncStatusUntilComplete(c, e.Ctx.BKECluster); err != nil {
		log.Error(constant.WorkerNodeSkipReason, "Failed to sync status for skipped nodes: %v", err)
	}

	log.Warn(constant.WorkerNodeSkipReason, "=========================================")
}

// logFailedNodesSummary 记录失败节点的概要信息
func (e *EnsureWorkerJoin) logFailedNodesSummary(
	log *bkev1beta1.BKELogger, successNodes, failedNodes bkenode.Nodes) {

	log.Warn(constant.WorkerNodeSkipReason, "=========================================")
	log.Warn(constant.WorkerNodeSkipReason, "Some worker nodes failed to join cluster")
	log.Warn(constant.WorkerNodeSkipReason, "=========================================")
	log.Warn(constant.WorkerNodeSkipReason,
		"Failed nodes count: %d, Success nodes count: %d, Total: %d",
		len(failedNodes), len(successNodes), len(e.nodesToJoin))
}

// markFailedNodesAsSkipped 标记失败节点为跳过状态
func (e *EnsureWorkerJoin) markFailedNodesAsSkipped(
	log *bkev1beta1.BKELogger, failedNodes bkenode.Nodes) {
	nodeFetcher := e.Ctx.NodeFetcher()
	for _, node := range failedNodes {
		nodeFetcher.SetNodeNeedSkip(e.Ctx, e.Ctx.BKECluster.Namespace, e.Ctx.BKECluster.Name, node.IP, true)
		nowNode, _ := nodeFetcher.GetNodeByIP(e.Ctx, e.Ctx.BKECluster.Namespace, e.Ctx.BKECluster.Name, node.IP)
		log.Warn(constant.WorkerNodeSkipReason,
			"Skipping failed worker node: %s (state: %s)",
			phaseutil.NodeInfo(node), nowNode.Status.State)
	}
}

// logFailedNodesGuidance 记录失败节点的处理指引
func (e *EnsureWorkerJoin) logFailedNodesGuidance(log *bkev1beta1.BKELogger) {
	log.Warn(constant.WorkerNodeSkipReason,
		"These nodes have been marked as 'NeedSkip' and will be excluded from subsequent operations")
	log.Info(constant.WorkerNodeSkipReason,
		"You can check the BKEAgent log on failed nodes (/var/log/openFuyao/bkeagent.log) to troubleshoot the issue")
	log.Info(constant.WorkerNodeSkipReason,
		"Or you can delete the failed BKENode resource and re-add them later")
}

// determineDeploymentResult 根据成功和失败节点数量决定部署结果
func (e *EnsureWorkerJoin) determineDeploymentResult(
	successNodes, failedNodes bkenode.Nodes, pollErr error) error {

	_, _, _, _, log := e.Ctx.Untie()

	// 如果有成功加入的节点，则认为集群可以继续
	if len(successNodes) > 0 {
		e.logSuccessResult(log, successNodes, failedNodes)
		return nil
	}

	// 如果所有节点都失败了，但不是超时错误，则返回错误
	if len(failedNodes) > 0 && !errors.Is(pollErr, wait.ErrWaitTimeout) {
		log.Error(constant.WorkerJoinFailedReason,
			"All worker nodes failed to join, error: %v", pollErr)
		return pollErr
	}

	// 如果是超时错误且没有成功节点，记录警告但不返回错误（让集群继续）
	if errors.Is(pollErr, wait.ErrWaitTimeout) && len(successNodes) == 0 && len(failedNodes) > 0 {
		e.logTimeoutResult(log, failedNodes)
		return nil
	}

	log.Info(constant.WorkerJoinSucceedReason, "Worker nodes join process completed successfully")
	return nil
}

// logSuccessResult 记录成功的部署结果
func (e *EnsureWorkerJoin) logSuccessResult(
	log *bkev1beta1.BKELogger, successNodes, failedNodes bkenode.Nodes) {

	log.Info(constant.WorkerJoinSucceedReason, "=========================================")
	log.Info(constant.WorkerJoinSucceedReason, "Worker nodes join completed successfully")
	log.Info(constant.WorkerJoinSucceedReason, "=========================================")
	log.Info(constant.WorkerJoinSucceedReason,
		"Success: %d nodes, Failed: %d nodes, Total: %d nodes",
		len(successNodes), len(failedNodes), len(e.nodesToJoin))

	if len(failedNodes) > 0 {
		log.Info(constant.WorkerJoinSucceedReason,
			"Cluster installation will continue with %d available worker node(s)",
			len(successNodes))
		log.Info(constant.WorkerJoinSucceedReason,
			"Failed nodes have been skipped and can be fixed later")
	}
	log.Info(constant.WorkerJoinSucceedReason, "=========================================")
}

// logTimeoutResult 记录超时的部署结果
func (e *EnsureWorkerJoin) logTimeoutResult(
	log *bkev1beta1.BKELogger, failedNodes bkenode.Nodes) {

	log.Warn(constant.WorkerNodeSkipReason, "=========================================")
	log.Warn(constant.WorkerNodeSkipReason, "Wait worker join timeout")
	log.Warn(constant.WorkerNodeSkipReason, "=========================================")
	log.Warn(constant.WorkerNodeSkipReason,
		"All %d worker node(s) have been skipped due to timeout or failures",
		len(failedNodes))
	log.Warn(constant.WorkerNodeSkipReason, "Cluster control plane is already initialized")
	log.Info(constant.WorkerNodeSkipReason,
		"Cluster installation will continue without these worker nodes")
	log.Info(constant.WorkerNodeSkipReason,
		"You can troubleshoot and add these nodes back to the cluster later")
	log.Warn(constant.WorkerNodeSkipReason, "=========================================")
}

func (e *EnsureWorkerJoin) pollWorkerJoinStatus(ctxTimeout context.Context) (*sync.Map, error) {
	_, _, _, _, log := e.Ctx.Untie()
	successJoinNode := sync.Map{}
	failedJoinNode := sync.Map{}

	pollCount := 0
	err := wait.PollImmediateUntil(1*time.Second, func() (done bool, err error) {
		pollCount++

		// 刷新 BKECluster 状态以获取最新的节点状态
		if err := e.Ctx.RefreshCtxBKECluster(); err != nil {
			log.Warn("pollWorkerJoinStatus", "Failed to refresh BKECluster: %v", err)
		}

		// 并发检查所有节点的状态
		e.checkAllNodesStatus(&successJoinNode, &failedJoinNode)

		// 检查是否所有节点都已处理完毕
		if done, success, failed := e.isAllNodesProcessed(&successJoinNode, &failedJoinNode); done {
			log.Info(constant.WorkerJoiningReason,
				"Wait worker join completed. success: %d, failed: %d, total: %d",
				success, failed, len(e.nodesToJoin))
			return true, nil
		}

		// 定期输出进度日志
		e.logProgressIfNeeded(pollCount, &successJoinNode, &failedJoinNode, log)
		return false, nil
	}, ctxTimeout.Done())
	return &successJoinNode, err
}

// checkAllNodesStatus 并发检查所有节点的加入状态
func (e *EnsureWorkerJoin) checkAllNodesStatus(successJoinNode, failedJoinNode *sync.Map) {
	wg := sync.WaitGroup{}
	for i, node := range e.nodesToJoin {
		wg.Add(1)
		go func(index int, n confv1beta1.Node) {
			defer wg.Done()
			e.checkSingleNodeStatus(index, n, successJoinNode, failedJoinNode)
		}(i, node)
	}
	wg.Wait()
}

// checkSingleNodeStatus 检查单个节点的加入状态
func (e *EnsureWorkerJoin) checkSingleNodeStatus(
	index int, n confv1beta1.Node, successJoinNode, failedJoinNode *sync.Map) {

	ctx, c, bkeCluster, _, log := e.Ctx.Untie()
	nodeFetcher := e.Ctx.NodeFetcher()

	// 已处理的节点直接跳过
	if _, ok := successJoinNode.Load(index); ok {
		return
	}
	if _, ok := failedJoinNode.Load(index); ok {
		return
	}

	// 检查节点是否被标记为失败（由 StatusManager 标记）
	failedFlag, _ := nodeFetcher.GetNodeStateFlagForCluster(ctx, bkeCluster, n.IP, bkev1beta1.NodeFailedFlag)
	if failedFlag {
		log.Warn(constant.WorkerNodeSkipReason,
			"Node %s has been marked with NodeFailedFlag, skip this node",
			phaseutil.NodeInfo(n))
		nodeFetcher.SetNodeNeedSkip(ctx, bkeCluster.Namespace, bkeCluster.Name, n.IP, true)
		failedJoinNode.Store(index, n)
		return
	}

	// 检查节点失败状态
	nowNode, _ := nodeFetcher.GetNodeByIP(ctx, bkeCluster.Namespace, bkeCluster.Name, n.IP)
	nodeState := nowNode.Status.State
	if nodeState == bkev1beta1.NodeBootStrapFailed || nodeState == bkev1beta1.NodeInitFailed {
		log.Warn(constant.WorkerNodeSkipReason,
			"Node %s is in failed state: %s, skip this node",
			phaseutil.NodeInfo(n), nodeState)
		nodeFetcher.SetNodeNeedSkip(ctx, bkeCluster.Namespace, bkeCluster.Name, n.IP, true)
		failedJoinNode.Store(index, n)
		return
	}

	// 检查节点是否成功加入
	machine, err := phaseutil.NodeToMachine(ctx, c, bkeCluster, n)
	if err != nil {
		return
	}
	if machine.Status.NodeRef != nil {
		successJoinNode.Store(index, n)
	}
}

// isAllNodesProcessed 检查是否所有节点都已处理完毕
func (e *EnsureWorkerJoin) isAllNodesProcessed(
	successJoinNode, failedJoinNode *sync.Map) (bool, int, int) {

	success := 0
	failed := 0
	successJoinNode.Range(func(key, value interface{}) bool { success++; return true })
	failedJoinNode.Range(func(key, value interface{}) bool { failed++; return true })

	if success+failed == len(e.nodesToJoin) {
		return true, success, failed
	}
	return false, success, failed
}

// logProgressIfNeeded 定期输出进度日志
func (e *EnsureWorkerJoin) logProgressIfNeeded(
	pollCount int, successJoinNode, failedJoinNode *sync.Map, log *bkev1beta1.BKELogger) {

	const logFrequency = 10
	if pollCount%logFrequency != 0 {
		return
	}

	success := 0
	failed := 0
	successJoinNode.Range(func(key, value interface{}) bool { success++; return true })
	failedJoinNode.Range(func(key, value interface{}) bool { failed++; return true })

	log.Info(constant.WorkerJoiningReason,
		"Wait worker join. success: %d, failed: %d, total: %d",
		success, failed, len(e.nodesToJoin))
}

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
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/statusmanage"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	labelhelper "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/label"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
)

// ProcessNodeMachineMappingResult 包含节点机器映射处理的结果
type ProcessNodeMachineMappingResult struct {
	DeleteMap     map[string]phaseutil.MachineAndNode
	WaitDeleteMap map[string]phaseutil.MachineAndNode
	NodesInfos    []string
	NodesCount    int
}

// ProcessNodeMachineMappingParams 包含 processNodeMachineMapping 方法的参数
type ProcessNodeMachineMappingParams struct {
	Ctx               context.Context
	Client            client.Client
	BKECluster        *bkev1beta1.BKECluster
	NodeFetcher       *nodeutil.NodeFetcher
	Nodes             bkenode.Nodes
	Log               *bkev1beta1.BKELogger
	NodeDeletedReason string
	NodeJoinedReason  string
}

// ProcessNodeMachineMapping 处理节点和机器的映射关系
func ProcessNodeMachineMapping(params ProcessNodeMachineMappingParams) (ProcessNodeMachineMappingResult, error) {
	ctx := params.Ctx
	c := params.Client
	bkeCluster := params.BKECluster
	nodes := params.Nodes
	log := params.Log
	var nodesInfos []string
	nodesCount := 0
	// machineName -> machineAndNode
	machineToNodeDeleteMap := make(map[string]phaseutil.MachineAndNode)
	machineToNodeWaitDeleteMap := make(map[string]phaseutil.MachineAndNode)

	// 遍历所有需要删除的节点，如果节点已经被删除了，需要从status中删除
	log.Debug("get associated machine for nodes, to avoid duplicate deletion")
	for _, node := range nodes {
		// 正常应该是能够找到machine的，如果找不到，说明节点已经被删除了，需要从status中删除
		if machine, err := phaseutil.NodeToMachine(ctx, c, bkeCluster, node); err != nil {
			log.Warn(params.NodeDeletedReason, "Node %s has not been associated with a Machine, skip delete it", phaseutil.NodeInfo(node))
			// 如果节点已经在status中，但是没有关联machine，需要从status中删除
			if params.NodeFetcher != nil {
				if err := params.NodeFetcher.DeleteBKENodeForCluster(ctx, bkeCluster, node.IP); err != nil {
					log.Warn(params.NodeDeletedReason, "Delete BKENode for %s failed, err: %v", node.IP, err)
				}
			}
			// 将节点从状态管理器中删除
			statusmanage.BKEClusterStatusManager.RemoveSingleNodeStatusCache(bkeCluster, node.IP)
			// remove node from AppointmentDeletedNodesAnnotationKey
			patchFunc := func(cluster *bkev1beta1.BKECluster) {
				phaseutil.RemoveAppointmentDeletedNodes(cluster, node.IP)
			}
			if err = mergecluster.SyncStatusUntilComplete(c, bkeCluster, patchFunc); err != nil {
				log.Error(params.NodeJoinedReason, "Sync status failed. err: %v", err)
				return ProcessNodeMachineMappingResult{}, err
			}
		} else {
			if machine.Status.Phase == string(clusterv1.MachinePhaseDeleting) {
				log.Info(params.NodeDeletedReason, "node %s is in deleting phase, skip delete it", phaseutil.NodeInfo(node))
				machineToNodeWaitDeleteMap[machine.Name] = phaseutil.MachineAndNode{Machine: machine, Node: node}
				continue
			}
			if machine.Status.Phase == string(clusterv1.MachinePhaseDeleted) {
				log.Info(params.NodeDeletedReason, "node %s is in deleted phase, skip delete it", phaseutil.NodeInfo(node))
				continue
			}
			nodesInfos = append(nodesInfos, phaseutil.NodeInfo(node))
			nodesCount++
			machineToNodeDeleteMap[machine.Name] = phaseutil.MachineAndNode{
				Machine: machine,
				Node:    node,
			}
		}
	}

	return ProcessNodeMachineMappingResult{
		DeleteMap:     machineToNodeDeleteMap,
		WaitDeleteMap: machineToNodeWaitDeleteMap,
		NodesInfos:    nodesInfos,
		NodesCount:    nodesCount,
	}, nil
}

// ProcessCommandFailureParams 包含处理命令失败的参数
type ProcessCommandFailureParams struct {
	Context        context.Context
	Client         client.Client
	BKECluster     *bkev1beta1.BKECluster
	NodeFetcher    *nodeutil.NodeFetcher
	InitCommand    *agentv1beta1.Command
	InitNodeIp     *string
	FailedNodes    []string
	RefreshContext func() error
}

// ProcessCommandFailureResult 包含处理命令失败的结果
type ProcessCommandFailureResult struct {
	Done    bool
	Success bool
	Err     error
}

// ProcessCommandFailure 处理命令失败情况
func ProcessCommandFailure(params ProcessCommandFailureParams) ProcessCommandFailureResult {
	// 等两秒
	time.Sleep(time.Duration(MasterInitSleepSeconds) * time.Second)
	if err := params.RefreshContext(); err != nil {
		log.Error(constant.InternalErrorReason, "Refresh BKECluster obj %q failed, err: %v", utils.ClientObjNS(params.BKECluster), err)
		return ProcessCommandFailureResult{Done: false, Success: false, Err: err}
	}

	nodeHasFailedFlag, _ := params.NodeFetcher.GetNodeStateFlagForCluster(params.Context, params.BKECluster, *params.InitNodeIp, bkev1beta1.NodeFailedFlag)
	if nodeHasFailedFlag {
		_ = params.NodeFetcher.SetNodeStateWithMessageForCluster(params.Context, params.BKECluster, *params.InitNodeIp, bkev1beta1.NodeBootStrapFailed, "")
		//删除command
		if err := params.Client.Delete(params.Context, params.InitCommand); err != nil {
			log.Warn(constant.MasterNotInitReason, "Delete init command failed, err: %v", err)
			return ProcessCommandFailureResult{Done: false, Success: false, Err: nil}
		}
		return ProcessCommandFailureResult{Done: true, Success: false, Err: errors.Errorf("master node init failed, failed nodes: %v", params.FailedNodes)}
	}

	log.Warn(constant.MasterNotInitReason, "Master node init failed, failed nodes: %v", params.FailedNodes)
	log.Info(constant.MasterNotInitReason, "Trying to init master node again...")
	// get command owner bkeMachine
	ownerBkeMachineName := params.InitCommand.OwnerReferences[0].Name
	// update owner bkeMachine status for bootstrap again

	key := client.ObjectKey{
		Name:      ownerBkeMachineName,
		Namespace: params.InitCommand.Namespace,
	}

	ownerBkeMachine := &bkev1beta1.BKEMachine{}
	if err := params.Client.Get(params.Context, key, ownerBkeMachine); err != nil {
		log.Error(constant.MasterNotInitReason, "Get init command owner bkeMachine failed, err: %v", err)
		return ProcessCommandFailureResult{Done: false, Success: false, Err: err}
	}
	labelhelper.RemoveBKEMachineLabel(ownerBkeMachine, bkenode.MasterNodeRole)
	if err := params.Client.Update(params.Context, ownerBkeMachine); err != nil {
		log.Error(constant.MasterNotInitReason, "Update init command owner bkeMachine failed, err: %v", err)
		return ProcessCommandFailureResult{Done: false, Success: false, Err: err}
	}
	return ProcessCommandFailureResult{Done: false, Success: false, Err: errors.Errorf("master node init command run failed, failed nodes: %v", params.FailedNodes)}
}

// GetTargetClusterNodes gets nodes from the target k8s cluster.
// This is a shared function used by EnsureWorkerDelete and EnsureMasterDelete.
func GetTargetClusterNodes(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster) (bkenode.Nodes, error) {
	clientset, _, err := kube.GetTargetClusterClient(ctx, c, bkeCluster)
	if err != nil {
		return nil, err
	}

	nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var nodes bkenode.Nodes
	for _, n := range nodeList.Items {
		node := confv1beta1.Node{
			Hostname: n.Name,
			IP:       kube.GetNodeIP(&n),
			Role:     phaseutil.GetNodeRolesFromK8sNode(&n),
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	kubedrain "k8s.io/kubectl/pkg/drain"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/command"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	l "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
)

const (
	EnsureWorkerUpgradeName                  confv1beta1.BKEClusterPhase = "EnsureWorkerUpgrade"
	WorkerNodeHealthCheckPollIntervalSeconds                             = 2 // 工作节点健康检查轮询间隔（秒）
	WorkerNodeHealthCheckTimeoutMinutes                                  = 5 // 工作节点健康检查超时时间（分钟）
)

type EnsureWorkerUpgrade struct {
	phaseframe.BasePhase
}

// CreateUpgradeCommandParams 创建升级命令实例函数的参数
type CreateUpgradeCommandParams struct {
	Ctx         context.Context
	Namespace   string
	Client      client.Client
	Scheme      *runtime.Scheme
	OwnerObj    *bkev1beta1.BKECluster
	ClusterName string
	Node        *confv1beta1.Node
	BKEConfig   string
	Phase       confv1beta1.BKEClusterPhase
}

// createUpgradeCommand 创建升级命令实例
func createUpgradeCommand(params CreateUpgradeCommandParams) command.Upgrade {
	return command.Upgrade{
		BaseCommand: command.BaseCommand{
			Ctx:         params.Ctx,
			NameSpace:   params.Namespace,
			Client:      params.Client,
			Scheme:      params.Scheme,
			OwnerObj:    params.OwnerObj,
			ClusterName: params.ClusterName,
			Unique:      true,
		},
		Node:      params.Node,
		BKEConfig: params.BKEConfig,
		Phase:     params.Phase,
	}
}

func NewEnsureWorkerUpgrade(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	ctx.Log.NormalLogger = l.Named(EnsureWorkerUpgradeName.String())
	base := phaseframe.NewBasePhase(ctx, EnsureWorkerUpgradeName)
	return &EnsureWorkerUpgrade{BasePhase: base}
}

func (e *EnsureWorkerUpgrade) ExecutePreHook() error {
	return e.BasePhase.DefaultPreHook()
}

func (e *EnsureWorkerUpgrade) Execute() (ctrl.Result, error) {
	if v, ok := annotation.HasAnnotation(e.Ctx.BKECluster, "deployAction"); !ok || v != "k8s_upgrade" {
		//添加boc所需的注解
		patchFunc := func(bkeCluster *bkev1beta1.BKECluster) {
			annotation.SetAnnotation(bkeCluster, "deployAction", "k8s_upgrade")
		}
		if err := mergecluster.SyncStatusUntilComplete(e.Ctx.Client, e.Ctx.BKECluster, patchFunc); err != nil {
			return ctrl.Result{}, err
		}
	}

	return e.reconcileWorkerUpgrade()
}

func (e *EnsureWorkerUpgrade) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}

	// cluster状态不正常，不需要执行
	if new.Status.ClusterStatus == bkev1beta1.ClusterUnhealthy || new.Status.ClusterStatus == bkev1beta1.ClusterUnknown {
		return false
	}

	bkeNodes, ok := fetchBKENodesIfCPInitialized(e.Ctx, new)
	if !ok {
		return false
	}
	nodes := phaseutil.GetNeedUpgradeWorkerNodesWithBKENodes(new, bkeNodes)
	if nodes.Length() == 0 {
		return false
	}

	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

func (e *EnsureWorkerUpgrade) reconcileWorkerUpgrade() (ctrl.Result, error) {
	_, _, bkeCluster, _, log := e.Ctx.Untie()
	if bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion != bkeCluster.Status.KubernetesVersion {
		ret, err := e.rolloutUpgrade()
		if err != nil {
			return ret, err
		}
	}
	log.Info(constant.WorkerUpgradedReason, "k8s version same, not need to upgrade work node")
	return ctrl.Result{}, nil
}

// PrepareUpgradeNodesParams 包含 prepareUpgradeNodes 函数的参数
type PrepareUpgradeNodesParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Log        *bkev1beta1.BKELogger
}

// prepareUpgradeNodes 准备需要升级的节点
func (e *EnsureWorkerUpgrade) prepareUpgradeNodes(params PrepareUpgradeNodesParams) (bkenode.Nodes, error) {
	needUpgradeNodes := bkenode.Nodes{}

	// Use NodeFetcher to get BKENodes from API server
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(params.Ctx, params.BKECluster)
	if err != nil {
		params.Log.Warn(constant.WorkerUpgradingReason, "failed to get BKENodes: %v", err)
		return nil, errors.Wrap(err, "failed to get BKENodes")
	}
	nodes := phaseutil.GetNeedUpgradeWorkerNodesWithBKENodes(params.BKECluster, bkeNodes)
	for _, node := range nodes {
		stateFlag, _ := e.Ctx.NodeFetcher().GetNodeStateFlag(params.Ctx,
			params.BKECluster.Namespace, params.BKECluster.Name, node.IP, bkev1beta1.NodeAgentReadyFlag)
		if !stateFlag {
			params.Log.Info(constant.WorkerUpgradingReason, "agent is not ready at node %s, skip upgrade", phaseutil.NodeInfo(node))
			continue
		}
		needUpgradeNodes = append(needUpgradeNodes, node)
	}

	if len(needUpgradeNodes) == 0 {
		params.Log.Info(constant.WorkerUpgradeFailedReason, "all the master node BKEAgent is not ready")
		return nil, errors.New("all the master node BKEAgent is not ready")
	}

	return needUpgradeNodes, nil
}

// ProcessNodeUpgradeParams 包含 processNodeUpgrade 函数的参数
type ProcessNodeUpgradeParams struct {
	Ctx              context.Context
	Client           client.Client
	BKECluster       *bkev1beta1.BKECluster
	ClientSet        kubernetes.Interface
	NeedUpgradeNodes bkenode.Nodes
	Drainer          *kubedrain.Helper
	Log              *bkev1beta1.BKELogger
}

// processNodeUpgrade 处理节点升级
func (e *EnsureWorkerUpgrade) processNodeUpgrade(params ProcessNodeUpgradeParams) (ctrl.Result, []string, error) {
	var failedUpgradeNodes []string
	nodeFetcher := e.Ctx.NodeFetcher()

	clientSet, _, _ := kube.GetTargetClusterClient(params.Ctx, params.Client, params.BKECluster)
	for _, node := range params.NeedUpgradeNodes {
		remoteNode, err := phaseutil.GetRemoteNodeByBKENode(params.Ctx, clientSet, node)
		if err != nil {
			params.Log.Error(constant.WorkerUpgradeFailedReason, "get remote cluster Node resource failed, err: %v", err)
			return ctrl.Result{}, nil, errors.Errorf("get remote cluster Node resource failed, err: %v", err)
		}
		// 已经是期望版本的节点不需要升级
		if remoteNode.Status.NodeInfo.KubeletVersion == params.BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion {
			params.Log.Info(constant.MasterUpgradeSucceedReason, "node %q is already the expected version %q, skip upgrade", phaseutil.NodeInfo(node), params.BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion)
			continue
		}
		// mark node as upgrading
		nodeFetcher.SetNodeStateWithMessage(params.Ctx, params.BKECluster.Namespace, params.BKECluster.Name,
			node.IP, bkev1beta1.NodeUpgrading, "Upgrading")
		if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
			return ctrl.Result{}, nil, err
		}

		if err := e.upgradeNode(node, remoteNode, params.Drainer); err != nil {
			failedUpgradeNodes = append(failedUpgradeNodes, phaseutil.NodeInfo(node))
			params.Log.Warn(constant.WorkerUpgradeFailedReason, "upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
			nodeFetcher.SetNodeStateWithMessage(params.Ctx, params.BKECluster.Namespace, params.BKECluster.Name,
				node.IP, bkev1beta1.NodeUpgradeFailed, err.Error())
			if err = mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
				return ctrl.Result{}, nil, err
			}
			continue
		}
		// mark node as upgrading success
		nodeFetcher.SetNodeStateWithMessage(params.Ctx, params.BKECluster.Namespace, params.BKECluster.Name,
			node.IP, bkev1beta1.NodeNotReady, "Upgrading success")
		if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
			return ctrl.Result{}, nil, err
		}
	}
	return ctrl.Result{}, failedUpgradeNodes, nil
}

func (e *EnsureWorkerUpgrade) rolloutUpgrade() (ctrl.Result, error) {
	ctx, c, bkeCluster, _, log := e.Ctx.Untie()

	// 准备需要升级的节点
	prepareParams := PrepareUpgradeNodesParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Log:        log,
	}
	needUpgradeNodes, err := e.prepareUpgradeNodes(prepareParams)
	if err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	// 对节点依次升级
	log.Info(constant.WorkerUpgradingReason, "Start upgrade worker nodes process, upgrade policy: rollingUpgrade")

	clientSet, _, _ := kube.GetTargetClusterClient(ctx, c, bkeCluster)
	drainer := phaseutil.NewDrainer(ctx, clientSet, nil, false, log)

	// 处理节点升级
	upgradeParams := ProcessNodeUpgradeParams{
		Ctx:              ctx,
		Client:           c,
		BKECluster:       bkeCluster,
		ClientSet:        clientSet,
		NeedUpgradeNodes: needUpgradeNodes,
		Drainer:          drainer,
		Log:              log,
	}
	_, failedUpgradeNodes, err := e.processNodeUpgrade(upgradeParams)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(failedUpgradeNodes) == 0 {
		log.Info(constant.WorkerUpgradeSucceedReason, "upgrade all worker success")
		return ctrl.Result{}, nil
	} else {
		log.Warn(constant.WorkerUpgradeFailedReason, "upgrade worker process finished, but some nodes upgrade failed, will retry later nodes: %v", failedUpgradeNodes)
		// worker node没有升级成功，不允许进入下一阶段
		return ctrl.Result{}, errors.Errorf("upgrade worker process finished, but some nodes upgrade failed, will retry later nodes: %v", failedUpgradeNodes)
	}
}

// WaitForWorkerNodeHealthCheckParams 等待节点健康检查通过函数的参数
type WaitForWorkerNodeHealthCheckParams struct {
	Ctx          context.Context
	ClientSet    kubernetes.Interface
	RemoteClient kube.RemoteKubeClient
	Node         confv1beta1.Node
	K8sVersion   string
	Logger       *bkev1beta1.BKELogger
}

// waitForWorkerNodeHealthCheck 等待节点健康检查通过
func waitForWorkerNodeHealthCheck(params WaitForWorkerNodeHealthCheckParams) error {
	return wait.PollWithContext(params.Ctx, time.Duration(WorkerNodeHealthCheckPollIntervalSeconds)*time.Second, time.Duration(WorkerNodeHealthCheckTimeoutMinutes)*time.Minute, func(ctx context.Context) (bool, error) {
		if utils.CtxDone(ctx) {
			return false, nil
		}
		remoteNode, err := params.ClientSet.CoreV1().Nodes().Get(ctx, params.Node.Hostname, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if err := params.RemoteClient.NodeHealthCheck(remoteNode, params.K8sVersion, params.Logger.NormalLogger); err != nil {
			return false, nil
		}
		return true, nil
	})
}

// executeNodeUpgrade 执行节点升级
func (e *EnsureWorkerUpgrade) executeNodeUpgrade(params ExecuteNodeUpgradeParams) error {
	createParams := CreateUpgradeCommandParams{
		Ctx:         params.Ctx,
		Namespace:   params.BKECluster.Namespace,
		Client:      params.Client,
		Scheme:      params.Scheme,
		OwnerObj:    params.BKECluster,
		ClusterName: params.BKECluster.Name,
		Node:        &params.Node,
		BKEConfig:   params.BKECluster.Name,
		Phase:       bkev1beta1.UpgradeWorker,
	}
	upgrade := createUpgradeCommand(createParams)
	upgrade.BackUpEtcd = false

	params.Log.Info(constant.WorkerUpgradingReason, "start upgrade node %s", phaseutil.NodeInfo(params.Node))
	if err := upgrade.New(); err != nil {
		params.Log.Error(constant.WorkerUpgradeFailedReason, "upgrade node %q failed: %v", phaseutil.NodeInfo(params.Node), err)
		return errors.Errorf("create upgrade command，node: %q failed: %v", phaseutil.NodeInfo(params.Node), err)
	}
	params.Log.Info(constant.WorkerUpgradingReason, "wait upgrade node %s finish", phaseutil.NodeInfo(params.Node))
	err, _, failedNodes := upgrade.Wait()
	if err != nil {
		params.Log.Error(constant.WorkerUpgradeFailedReason, "wait upgrade command complete failed，node: %q, err: %v", phaseutil.NodeInfo(params.Node), err)
		return errors.Errorf("wait upgrade command complete failed，node: %q, err: %v", phaseutil.NodeInfo(params.Node), err)
	}
	if len(failedNodes) != 0 {
		params.Log.Error(constant.WorkerUpgradeFailedReason, "upgrade node %q failed: %v", phaseutil.NodeInfo(params.Node), err)
		commandErrs, err := phaseutil.LogCommandFailed(*upgrade.Command, failedNodes, params.Log, constant.WorkerUpgradeFailedReason)
		phaseutil.MarkNodeStatusByCommandErrs(params.Ctx, params.Client, params.BKECluster, commandErrs)
		return errors.Errorf("upgrade node %q failed: %v", phaseutil.NodeInfo(params.Node), err)
	}
	params.Log.Info(constant.WorkerUpgradeSucceedReason, "upgrade node %q operation succeed", phaseutil.NodeInfo(params.Node))
	return nil
}

// WaitForNodeHealthParams 包含 waitForNodeHealth 函数的参数
type WaitForNodeHealthParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Node       confv1beta1.Node
	Log        *bkev1beta1.BKELogger
}

// waitForNodeHealth 等待节点健康检查
func (e *EnsureWorkerUpgrade) waitForNodeHealth(params WaitForNodeHealthParams) error {
	remoteClient, err := kube.NewRemoteClientByBKECluster(params.Ctx, params.Client, params.BKECluster)
	if err != nil {
		params.Log.Error(constant.WorkerUpgradeFailedReason, "get remote client for BKECluster %q failed", utils.ClientObjNS(params.BKECluster))
		return errors.Errorf("get remote client for BKECluster %q failed: %v", utils.ClientObjNS(params.BKECluster), err)
	}
	clientSet, _ := remoteClient.KubeClient()
	// wait for node pass healthy check
	params.Log.Info(constant.WorkerUpgradingReason, "wait for node %q pass healthy check", phaseutil.NodeInfo(params.Node))
	healthParams := WaitForWorkerNodeHealthCheckParams{
		Ctx:          params.Ctx,
		ClientSet:    clientSet,
		RemoteClient: remoteClient,
		Node:         params.Node,
		K8sVersion:   params.BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
		Logger:       params.Log,
	}
	err = waitForWorkerNodeHealthCheck(healthParams)

	if err != nil {
		params.Log.Error(constant.WorkerUpgradeFailedReason, "upgrade node %q failed: %v", phaseutil.NodeInfo(params.Node), err)
		return errors.Errorf("wait for node %q pass healthy check failed: %v", phaseutil.NodeInfo(params.Node), err)
	}
	params.Log.Info(constant.WorkerUpgradingReason, "upgrade worker node %q success", phaseutil.NodeInfo(params.Node))
	return nil
}

func (e *EnsureWorkerUpgrade) upgradeNode(node confv1beta1.Node, remoteNode *corev1.Node, drainer *kubedrain.Helper) error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()

	// 执行节点升级
	upgradeParams := ExecuteNodeUpgradeParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Scheme:     scheme,
		Node:       node,
		Log:        log,
	}
	if err := e.executeNodeUpgrade(upgradeParams); err != nil {
		return err
	}

	// 等待节点健康检查
	healthParams := WaitForNodeHealthParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Node:       node,
		Log:        log,
	}
	return e.waitForNodeHealth(healthParams)
}

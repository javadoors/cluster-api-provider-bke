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
	"fmt"
	"strings"

	"github.com/pkg/errors"
	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/command"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clusterutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/condition"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	EnsureLoadBalanceName confv1beta1.BKEClusterPhase = "EnsureLoadBalance"
)

type EnsureLoadBalance struct {
	phaseframe.BasePhase
}

func NewEnsureLoadBalance(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureLoadBalanceName)
	return &EnsureLoadBalance{
		BasePhase: base,
	}
}

func (e *EnsureLoadBalance) Execute() (ctrl.Result, error) {
	if err := e.ConfiguringLoadBalancer(); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (e *EnsureLoadBalance) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) (needExecute bool) {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}
	// 如果Status.Ready为false，需要配置负载均衡器
	if !new.Status.Ready {
		e.SetStatus(bkev1beta1.PhaseWaiting)
		return true
	}

	haveLoadBalance := true
	// 如果spec.ControlPlaneEndpoint.Host为master节点，不需要配置负载均衡器
	newNodes, _ := e.Ctx.NodeFetcher().GetNodesForBKECluster(e.Ctx, new)
	for _, node := range newNodes {
		if node.IP == new.Spec.ControlPlaneEndpoint.Host {
			haveLoadBalance = false
			break
		}
	}

	if !haveLoadBalance {
		return false
	}

	// 如果有master节点变更，需要配置负载均衡器
	// Use NodeFetcher to get BKENodes from API server
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, new)
	if err != nil {
		return false
	}
	nodes := phaseutil.GetNeedLoadBalanceNodesWithBKENodes(e.Ctx, e.Ctx.Client, new, bkeNodes)
	if len(nodes) == 0 {
		return false
	}

	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

// ConfiguringLoadBalancer configures a load balancer for the control plane endpoint.
func (e *EnsureLoadBalance) ConfiguringLoadBalancer() error {
	_, c, bkeCluster, _, log := e.Ctx.Untie()
	nodeFetcher := e.Ctx.NodeFetcher()
	allNodes, _ := nodeFetcher.GetNodesForBKECluster(e.Ctx, bkeCluster)

	nodes := allNodes.Master()

	if len(nodes) == 0 {
		log.Warn("ConfigureLoadBalancer", "no master nodes found")
		return errors.New("no master nodes found")
	}

	var errs []string
	for _, node := range nodes {
		nodeStateFlag, _ := nodeFetcher.GetNodeStateFlagForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeEnvFlag)
		if !nodeStateFlag {
			errs = append(errs, fmt.Sprintf("master node %s agent is not ready,cannot configure load balancer", node.IP))
		}
	}
	if len(errs) > 0 {
		log.Warn("ConfigureLoadBalancer", strings.Join(errs, ","))
		return errors.New(strings.Join(errs, ","))
	}

	_, extraLoadBalanceExist := bkeCluster.Spec.ClusterConfig.CustomExtra["extraLoadBalanceIP"]
	if clusterutil.AvailableLoadBalancerEndPoint(bkeCluster.Spec.ControlPlaneEndpoint, allNodes) && !extraLoadBalanceExist {
		if err := e.configureExternalLoadBalancer(nodes); err != nil {
			return err
		}
	} else {
		log.Debug("ControlPlaneEndpoint is a internal point")
		condition.ConditionMark(bkeCluster, bkev1beta1.ControlPlaneEndPointSetCondition, confv1beta1.ConditionTrue, constant.LoadBalancerNotConfiguredReason, "load balancer not configured")
		log.Warn(constant.LoadBalancerNotReadyReason, "Without load balancer configured, the cluster %q control plane endpoint is %q", bkeCluster.Name, bkeCluster.Spec.ControlPlaneEndpoint.String())
		bkeCluster.Status.Ready = true
	}

	if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
		return err
	}
	log.Debug("reconcile load balance success")
	return nil
}

// configureExternalLoadBalancer configures an external load balancer for the control plane endpoint.
func (e *EnsureLoadBalance) configureExternalLoadBalancer(nodes bkenode.Nodes) error {
	_, _, _, _, log := e.Ctx.Untie()
	log.Debug("ControlPlaneEndpoint is a external load balancer")
	log.Info("ConfigureLoadBalancer", "Start to configure load balancer for control plane endpoint")

	loadBalanceCommand, err := e.createLoadBalancerCommand(nodes)
	if err != nil {
		return err
	}
	if loadBalanceCommand == nil {
		return nil
	}

	return e.executeAndHandleLoadBalancer(loadBalanceCommand)
}

// createLoadBalancerCommand creates and initializes the HA load balancer command.
func (e *EnsureLoadBalance) createLoadBalancerCommand(nodes bkenode.Nodes) (*command.HA, error) {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()

	cfg := bkeinit.BkeConfig(*bkeCluster.Spec.ClusterConfig)
	loadBalanceCommand := command.HA{
		BaseCommand: command.BaseCommand{
			Ctx: ctx, NameSpace: bkeCluster.Namespace,
			Client:      c,
			Scheme:      scheme,
			OwnerObj:    bkeCluster,
			ClusterName: bkeCluster.Name,
			Unique:      true,
		},
		MasterNodes:              nodes.Master(),
		ControlPlaneEndpointPort: bkeCluster.Spec.ControlPlaneEndpoint.Port,
		ControlPlaneEndpointVIP:  bkeCluster.Spec.ControlPlaneEndpoint.Host,
		ThirdImageRepo:           cfg.ImageThirdRepo(),
		FuyaoImageRepo:           cfg.ImageFuyaoRepo(),
		ManifestsDir:             cfg.Cluster.Kubelet.ManifestsDir,
		VirtualRouterId:          cfg.CustomExtra["masterVirtualRouterId"],
	}

	log.Debug("step 3 configure load balancer, and new HA command")
	if err := loadBalanceCommand.New(); err != nil {
		errInfo := "failed to create k8s HA Command"
		log.Warn("ConfigureLoadBalancer", "%s: %v", errInfo, err)
		condition.ConditionMark(bkeCluster, bkev1beta1.ControlPlaneEndPointSetCondition, confv1beta1.ConditionFalse, constant.CommandCreateFailedReason, errInfo)
		return nil, nil
	}

	return &loadBalanceCommand, nil
}

// executeAndHandleLoadBalancer executes the load balancer command and handles the results.
func (e *EnsureLoadBalance) executeAndHandleLoadBalancer(loadBalanceCommand *command.HA) error {
	_, c, bkeCluster, _, log := e.Ctx.Untie()
	nodeFetecher := e.Ctx.NodeFetcher()

	log.Info(constant.LoadBalancerCreatingReason, "Waiting load balancer configured ready")
	err, successNodes, failedNodes := loadBalanceCommand.Wait()
	if err != nil {
		return errors.Errorf("failed to configure load balancer: %v", err)
	}

	for _, node := range failedNodes {
		nodeFetecher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, phaseutil.GetNodeIPFromCommandWaitResult(node), bkev1beta1.NodeInitFailed, "Failed to configure load balancer")
	}
	for _, node := range successNodes {
		nodeIP := phaseutil.GetNodeIPFromCommandWaitResult(node)
		nodeFetecher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, nodeIP, bkev1beta1.NodeInitializing, "Load balancer configured")
		nodeFetecher.MarkNodeStateFlagForCluster(e.Ctx, bkeCluster, nodeIP, bkev1beta1.NodeHAFlag)
	}

	if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
		return err
	}

	if len(failedNodes) > 0 {
		log.Warn(constant.LoadBalancerNotReadyReason, "failed to configure load balancer: %v", err)
		log.Warn(constant.LoadBalancerNotReadyReason, "The load balancer configured failed on the following Nodes %v", failedNodes, ",")
		commandErrs, err := phaseutil.LogCommandFailed(*loadBalanceCommand.Command, failedNodes, log, constant.LoadBalancerNotReadyReason)
		phaseutil.MarkNodeStatusByCommandErrs(e.Ctx, e.Ctx.Client, bkeCluster, commandErrs)
		log.Warn(constant.LoadBalancerNotReadyReason, "Load balancer configured failed, you can check the BKEAgent log on the error node (/var/log/openFuyao/bkeagent.log) and manually resolve the problem. Then restart the BKEAgent on the node")
		return errors.Errorf("failed to configure load balancer, loadBalanceCommand run failed in flow nodes: %v, err: %v", strings.Join(failedNodes, ","), err)
	}

	endpoint := bkeCluster.Spec.ControlPlaneEndpoint.String()
	log.Info(constant.LoadBalancerReadyReason, "The load balancer was configured on the following Nodes %v", successNodes)
	log.Info(constant.LoadBalancerReadyReason, "The cluster %q control plane endpoint will be %q", bkeCluster.Name, endpoint)
	condition.ConditionMark(bkeCluster, bkev1beta1.ControlPlaneEndPointSetCondition, confv1beta1.ConditionTrue, constant.LoadBalancerReadyReason, "")
	bkeCluster.Status.Ready = true
	return nil
}

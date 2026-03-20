/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package phases

import (
	"fmt"

	"sigs.k8s.io/cluster-api/util/version"
	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/command"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clusterutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	EnsureContainerdUpgradeName confv1beta1.BKEClusterPhase = "EnsureContainerdUpgrade"
)

type EnsureContainerdUpgrade struct {
	phaseframe.BasePhase
}

func NewEnsureContainerdUpgrade(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureContainerdUpgradeName)
	return &EnsureContainerdUpgrade{BasePhase: base}
}

// Execute 执行具体的升级操作
func (e *EnsureContainerdUpgrade) Execute() (ctrl.Result, error) {
	return e.rolloutContainerd()
}

func (e *EnsureContainerdUpgrade) getCommand() *command.ENV {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()
	log.Error(constant.ContainerdUpgradingReason, "cur bke cluster is : %v", bkeCluster)
	// Use NodeFetcher to get BKENodes from API server
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
	if err != nil {
		log.Error(constant.ContainerdUpgradingReason, "failed to get BKENodes: %v", err)
		return nil
	}
	exceptEnvNodes := phaseutil.GetNeedUpgradeNodesWithBKENodes(bkeCluster, bkeNodes)

	nodes, err := e.Ctx.GetNodes()
	if err != nil {
		log.Error(constant.ContainerdUpgradingReason, "failed to get nodes: %v", err)
		return nil
	}

	var extra []string
	var extraHosts []string
	if clusterutil.AvailableLoadBalancerEndPoint(bkeCluster.Spec.ControlPlaneEndpoint, nodes) {
		extra = append(extra, bkeCluster.Spec.ControlPlaneEndpoint.Host)
		extraHosts = append(extraHosts, fmt.Sprintf("%s:%s", constant.MasterHADomain, bkeCluster.Spec.ControlPlaneEndpoint.Host))
	}
	ingressVip, _ := clusterutil.GetIngressConfig(bkeCluster.Spec.ClusterConfig.Addons)
	if ingressVip != "" && ingressVip != bkeCluster.Spec.ControlPlaneEndpoint.Host {
		extra = append(extra, ingressVip)
	}

	params := BuildCommonEnvCommandParams{
		Ctx:            ctx,
		Client:         c,
		BKECluster:     bkeCluster,
		Scheme:         scheme,
		ExceptEnvNodes: exceptEnvNodes,
		Extra:          extra,
		ExtraHosts:     extraHosts,
		DryRun:         bkeCluster.Spec.DryRun,
		Log:            log,
	}

	envCmd, err := BuildCommonEnvCommand(params)
	if err != nil {
		log.Error(constant.ContainerdUpgradingReason, "failed to build common env command: %v", err)
		return nil
	}

	return envCmd
}

func (e *EnsureContainerdUpgrade) resetContainerd() error {
	_, _, _, _, log := e.Ctx.Untie()
	envCommand := e.getCommand()
	if envCommand == nil {
		return fmt.Errorf("new containerd command fail")
	}
	if err := envCommand.NewConatinerdReset(); err != nil {
		errInfo := fmt.Sprintf("failed to create k8s containerd reset: %v", err)
		log.Error(constant.ContainerdUpgradeFailed, errInfo)
		return err
	}

	log.Info(constant.ContainerdUpgradingReason, "Waiting for the k8s containerd reset to complete")
	err, successNodes, failedNodes := envCommand.Wait()
	if err != nil || len(failedNodes) > 0 {
		errInfo := fmt.Sprintf("failed to reset k8s containerd: %d/%d", len(successNodes), len(failedNodes)+len(successNodes))
		log.Error(constant.ContainerdUpgradeFailed, errInfo)
		return err
	}
	return nil
}

func (e *EnsureContainerdUpgrade) redeployContainerd() error {
	_, _, _, _, log := e.Ctx.Untie()
	envCommand := e.getCommand()
	if envCommand == nil {
		return fmt.Errorf("new containerd command fail")
	}
	if err := envCommand.NewConatinerdRedeploy(); err != nil {
		errInfo := fmt.Sprintf("failed to create k8s containerd redeploy: %v", err)
		log.Error(constant.ContainerdUpgradeFailed, errInfo)
		return err
	}

	log.Info(constant.ContainerdUpgradingReason, "Waiting for the k8s containerd redeploy to complete")
	err, successNodes, failedNodes := envCommand.Wait()
	if err != nil || len(failedNodes) > 0 {
		errInfo := fmt.Sprintf("failed to redeploy k8s containerd: %d/%d", len(successNodes), len(failedNodes)+len(successNodes))
		log.Error(constant.ContainerdUpgradeFailed, errInfo)
		return err
	}
	return nil
}

func (e *EnsureContainerdUpgrade) rolloutContainerd() (ctrl.Result, error) {
	_, _, bkeCluster, _, log := e.Ctx.Untie()

	err := e.resetContainerd()
	if err != nil {
		return ctrl.Result{}, err
	}
	err = e.redeployContainerd()
	if err != nil {
		return ctrl.Result{}, err
	}

	log.Info(constant.ContainerdUpgradeSuccess, "upgrade containerd success")
	bkeCluster.Status.ContainerdVersion = bkeCluster.Spec.ClusterConfig.Cluster.ContainerdVersion

	return ctrl.Result{}, nil
}

func (e *EnsureContainerdUpgrade) isContainerdNeedUpgrade(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	newConfig := new.Spec.ClusterConfig
	if new.Status.ContainerdVersion == "" {
		return false
	}

	oldv, err := version.ParseMajorMinorPatch(new.Status.ContainerdVersion)
	if err != nil {
		return false
	}
	newv, err := version.ParseMajorMinorPatch(newConfig.Cluster.ContainerdVersion)
	if err != nil {
		return false
	}
	// step 2 compare cluster version upgrade
	switch version.Compare(newv, oldv) {
	case -1:
	case 0:
	case 1:
		bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesForBKECluster(e.Ctx.Context, e.Ctx.BKECluster)
		if err != nil {
			return false
		}
		statusNodes := bkev1beta1.NewBKENodes(bkeNodes)
		for _, node := range statusNodes {
			if node.Status.NeedSkip {
				continue
			}
			if statusNodes.GetNodeStateFlag(node.Spec.IP, bkev1beta1.NodeFailedFlag) {
				continue
			}
			return true
		}
	default:
		return false
	}
	return false
}

// NeedExecute 这个阶段，只有在初始新建补丁版本时才需要执行，如何判断是初始新建补丁版本？old为空，new中openFuyao version带小版本
func (e *EnsureContainerdUpgrade) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}
	if !e.isContainerdNeedUpgrade(old, new) {
		return false
	}
	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

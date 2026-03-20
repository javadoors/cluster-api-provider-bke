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

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/command"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clusterutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	EnsureDryRunName confv1beta1.BKEClusterPhase = "EnsureDryRun"
)

type EnsureDryRun struct {
	phaseframe.BasePhase
}

func NewEnsureDryRun(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureDryRunName)
	return &EnsureDryRun{
		BasePhase: base,
	}
}

func (e *EnsureDryRun) Execute() (ctrl.Result, error) {

	if err := e.reconcileDryRun(); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (e *EnsureDryRun) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !new.DeletionTimestamp.IsZero() {
		return false
	}
	if new.Spec.Pause {
		return false
	}

	if !new.Spec.DryRun {
		return false
	}
	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

func (e *EnsureDryRun) reconcileDryRun() error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()

	// only check env and push agent ,do not real change any BKECluster Status
	if bkeCluster.Annotations == nil {
		bkeCluster.Annotations = make(map[string]string)
	}
	annotations := bkeCluster.GetAnnotations()

	if !bkeCluster.Spec.DryRun {
		return e.handleDryRunDisabled(c, bkeCluster, annotations, log)
	}

	nodes, err := e.getDryRunNodes(bkeCluster, annotations, log)
	if err != nil {
		return err
	}

	if nodes.Length() == 0 {
		log.Info(constant.DryRunReason, "Nodes not changed, no need to dry run")
		return nil
	}

	if err := e.updateDryRunAnnotation(c, bkeCluster, nodes, annotations, log); err != nil {
		return err
	}

	// push agent
	pushAgentParams := PushAgentParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Nodes:      nodes,
		Log:        log,
	}
	if err := e.pushAgentWithParams(pushAgentParams); err != nil {
		return err
	}

	// pingBkeAgent
	if err := e.checkBKEAgentStatus(log); err != nil {
		return err
	}

	log.Info(constant.DryRunReason, "BKEAgent is ready on all Nodes")

	// check env
	checkNodeEnvParams := CheckNodeEnvironmentParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Scheme:     scheme,
		Nodes:      nodes,
		Log:        log,
	}
	return e.checkNodeEnvironmentWithParams(checkNodeEnvParams)
}

// handleDryRunDisabled 处理dryRun禁用的情况
func (e *EnsureDryRun) handleDryRunDisabled(c client.Client, bkeCluster *bkev1beta1.BKECluster, annotations map[string]string, log *bkev1beta1.BKELogger) error {
	// remove dryRun annotation
	if _, ok := annotations[annotation.BKEClusterDryRunAnnotationKey]; ok {
		delete(annotations, annotation.BKEClusterDryRunAnnotationKey)
		bkeCluster.SetAnnotations(annotations)
		if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
			log.Finish(constant.DryRunReason, "failed to update BKECluster Status: %v", err)
			return nil
		}
	}
	return nil
}

// getDryRunNodes 获取需要dryRun的节点
func (e *EnsureDryRun) getDryRunNodes(bkeCluster *bkev1beta1.BKECluster, annotations map[string]string, log *bkev1beta1.BKELogger) (bkenode.Nodes, error) {
	// Use NodeFetcher to get BKENodes from API server
	allBkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
	if err != nil {
		log.Warn(constant.DryRunReason, "failed to get BKENodes: %v", err)
		return nil, err
	}
	bkeNodes := phaseutil.GetNeedInitEnvNodesWithBKENodes(bkeCluster, allBkeNodes)

	nodes := bkenode.Nodes{}
	markNodes, ok := annotations[annotation.BKEClusterDryRunAnnotationKey]
	if ok {
		for _, node := range bkeNodes {
			if !strings.Contains(markNodes, node.IP) {
				nodes = append(nodes, node)
			}
		}
	} else {
		nodes = bkeNodes
	}

	return nodes, nil
}

// updateDryRunAnnotation 更新dryRun注解
func (e *EnsureDryRun) updateDryRunAnnotation(c client.Client, bkeCluster *bkev1beta1.BKECluster, nodes bkenode.Nodes, annotations map[string]string, log *bkev1beta1.BKELogger) error {
	// add annotation to BKECluster to record dryRun Nodes toavoid reconcile again
	dryRunNodes := ""
	for _, node := range nodes {
		dryRunNodes += node.IP + ","
	}

	// 检查annotations是否为nil，避免空指针解引用
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[annotation.BKEClusterDryRunAnnotationKey] = dryRunNodes
	bkeCluster.SetAnnotations(annotations)
	if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
		log.Finish(constant.DryRunReason, "failed to update BKECluster Status: %v", err)
		return err
	}
	return nil
}

// PushAgentParams 包含推送BKEAgent所需的参数
type PushAgentParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Nodes      bkenode.Nodes
	Log        *bkev1beta1.BKELogger
}

// pushAgent 推送BKEAgent到节点
func (e *EnsureDryRun) pushAgent(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, nodes bkenode.Nodes, log *bkev1beta1.BKELogger) error {
	params := PushAgentParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Nodes:      nodes,
		Log:        log,
	}
	return e.pushAgentWithParams(params)
}

// pushAgentWithParams 使用参数结构体推送BKEAgent到节点
func (e *EnsureDryRun) pushAgentWithParams(params PushAgentParams) error {
	var nodeNames []string
	for _, node := range params.Nodes {
		nodeNames = append(nodeNames, phaseutil.NodeInfo(node))
	}
	params.Log.Info(constant.DryRunReason, "Start push BKEAgent to nodes(s), %q", nodeNames)
	localKubeConfigSecret := &corev1.Secret{}
	if err := params.Client.Get(params.Ctx, constant.GetLocalKubeConfigObjectKey(), localKubeConfigSecret); err != nil {
		if apierrors.IsNotFound(err) {
			params.Log.Error(constant.DryRunReason, "Local kubeconfig secret not found")
			return nil
		}
		params.Log.Error(constant.DryRunReason, "Failed to get local kubeconfig secret, err：%v", err)
		return nil
	}
	localKubeConfig := localKubeConfigSecret.Data["config"]
	params.Log.Info(constant.DryRunReason, "Push BKEAgent will take some time, please wait")
	hosts := phaseutil.NodeToRemoteHost(params.Nodes)
	ntpServer := params.BKECluster.Spec.ClusterConfig.Cluster.NTPServer
	if failedNodes := phaseutil.PushAgent(hosts, localKubeConfig, ntpServer); len(failedNodes) > 0 {
		errInfo := "Failed to push bkeagent to flowing Nodes"
		params.Log.Error(constant.DryRunReason, "%s: %v", errInfo, failedNodes)
		// todo retry to push bkeagent to failed Nodes ?
		return nil
	}
	params.Log.Info(constant.DryRunReason, "Successfully pushed BKEAgent to all Nodes")
	return nil
}

// checkBKEAgentStatus 检查BKEAgent状态
func (e *EnsureDryRun) checkBKEAgentStatus(log *bkev1beta1.BKELogger) error {
	err, _, failedNodes := e.pingBKEAgent()
	if err != nil {
		log.Error(constant.DryRunReason, "Failed to ping BKEAgent, err: %v", err)
		return nil
	}

	if len(failedNodes) > 0 {
		errInfo := fmt.Sprintf("Failed to ping bkeagent on flow Nodes: %v", failedNodes)
		log.Error(constant.DryRunReason, errInfo)
		return nil
	}
	return nil
}

// CheckNodeEnvironmentParams 包含检查节点环境所需的参数
type CheckNodeEnvironmentParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Scheme     *runtime.Scheme
	Nodes      bkenode.Nodes
	Log        *bkev1beta1.BKELogger
}

// checkNodeEnvironmentWithParams 使用参数结构体检查节点环境
func (e *EnsureDryRun) checkNodeEnvironmentWithParams(params CheckNodeEnvironmentParams) error {
	//check env
	// check and init node env for k8s
	params.Log.Info(constant.DryRunReason, "Start check and init node env for k8s")

	var extra []string
	allNodes, _ := e.Ctx.NodeFetcher().GetNodesForBKECluster(params.Ctx, params.BKECluster)
	if clusterutil.AvailableLoadBalancerEndPoint(params.BKECluster.Spec.ControlPlaneEndpoint, allNodes) {
		extra = append(extra, params.BKECluster.Spec.ControlPlaneEndpoint.Host)
	}
	envCommand := command.ENV{
		BaseCommand: command.BaseCommand{
			Ctx:         params.Ctx,
			NameSpace:   params.BKECluster.Namespace,
			Client:      params.Client,
			Scheme:      params.Scheme,
			OwnerObj:    params.BKECluster,
			ClusterName: params.BKECluster.Name,
			Unique:      true,
		},
		Nodes:         params.Nodes,
		BkeConfigName: params.BKECluster.Name,
		Extra:         extra,
		DryRun:        params.BKECluster.Spec.DryRun,
	}
	if err := envCommand.New(); err != nil {
		errInfo := "Failed to create k8s env init command"
		params.Log.Error(constant.DryRunReason, "%s: %v", errInfo, err)
		return err
	}
	params.Log.Info(constant.DryRunReason, "Waiting for the env check to complete")

	// wait command finish
	err, _, failedNodes := envCommand.Wait()
	if err != nil {
		return errors.Wrap(err, "failed to wait env init command")
	}
	if len(failedNodes) > 0 {
		errInfo := fmt.Sprintf("Failed to check env on flow Nodes: %v", failedNodes)
		params.Log.Finish(constant.DryRunReason, errInfo)
		return nil
	}

	return nil
}

func (e *EnsureDryRun) pingBKEAgent() (error, []string, []string) {
	ctx, c, bkeCluster, scheme, _ := e.Ctx.Untie()
	err, success, failed := phaseutil.PingBKEAgent(ctx, c, scheme, bkeCluster)
	return err, success, failed
}

/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
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
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/command"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/condition"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	EnsureNodesPostProcessName confv1beta1.BKEClusterPhase = "EnsureNodesPostProcess"
)

type EnsureNodesPostProcess struct {
	phaseframe.BasePhase
	nodes bkenode.Nodes
}

func NewEnsureNodesPostProcess(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureNodesPostProcessName)
	return &EnsureNodesPostProcess{BasePhase: base}
}

func (e *EnsureNodesPostProcess) Execute() (ctrl.Result, error) {
	return e.CheckOrRunPostProcess()
}

func (e *EnsureNodesPostProcess) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}

	// Use NodeFetcher to get BKENodes from API server (in controller context)
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, new)
	if err != nil {
		return false
	}

	needExecute := phaseutil.GetNeedPostProcessNodesWithBKENodes(new, bkeNodes).Length() > 0
	if needExecute {
		e.SetStatus(bkev1beta1.PhaseWaiting)
	}
	return needExecute
}

func (e *EnsureNodesPostProcess) CheckOrRunPostProcess() (ctrl.Result, error) {
	_, _, bkeCluster, _, log := e.Ctx.Untie()
	log.Info(constant.NodesPostProcessCheckingReason, "Start post process scripts")

	// Use NodeFetcher to get BKENodes from API server (in controller context)
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	nodes := phaseutil.GetNeedPostProcessNodesWithBKENodes(bkeCluster, bkeNodes)
	if nodes.Length() == 0 {
		condition.ConditionMark(bkeCluster, bkev1beta1.NodesPostProcessCondition, confv1beta1.ConditionTrue, constant.NodesPostProcessReadyReason, "")
		log.Info(constant.NodesPostProcessCheckingReason, "No nodes need post process")
		return ctrl.Result{}, nil
	}
	e.nodes = nodes

	condition.ConditionMark(bkeCluster, bkev1beta1.NodesPostProcessCondition, confv1beta1.ConditionFalse, constant.NodesPostProcessNotReadyReason, "")
	if err := e.executeNodePostProcessScripts(); err != nil {
		return ctrl.Result{}, err
	}

	condition.ConditionMark(bkeCluster, bkev1beta1.NodesPostProcessCondition, confv1beta1.ConditionTrue, constant.NodesPostProcessReadyReason, "")
	log.Info(constant.NodesPostProcessReadyReason, "Post process scripts complete")
	return ctrl.Result{}, nil
}

func (e *EnsureNodesPostProcess) executeNodePostProcessScripts() error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()
	nodes := e.nodes
	log.Info(constant.NodesPostProcessCheckingReason, "Checking post process config, total nodes=%d", len(nodes))

	var nodesWithConfig bkenode.Nodes
	var nodesWithoutConfig bkenode.Nodes // 新增：收集没有配置的节点
	for _, node := range nodes {
		if node.IP == "" {
			log.Warn(constant.NodesPostProcessCheckingReason, "nodeIP empty, skip")
			continue
		}
		hasConfig := e.checkPostProcessConfigExists(ctx, c, log, node.IP)
		if !hasConfig {
			nodesWithoutConfig = append(nodesWithoutConfig, node)
			log.Warn(constant.NodesPostProcessCheckingReason, "node %s has no postprocess config, skip", node.IP)
			continue
		}
		nodesWithConfig = append(nodesWithConfig, node)
	}

	for _, node := range nodesWithoutConfig {
		nodeFetcher := e.Ctx.NodeFetcher()
		nodeFetcher.MarkNodeStateFlagForCluster(ctx, bkeCluster, node.IP, bkev1beta1.NodePostProcessFlag)
		nodeFetcher.SetBKENodeStateMessage(ctx, bkeCluster.Namespace, bkeCluster.Name, node.IP, "Post process skipped (no config)")
	}

	log.Info(constant.NodesPostProcessCheckingReason, "postprocess config check done, total=%d, hit=%d", len(nodes), len(nodesWithConfig))
	if len(nodesWithConfig) == 0 {
		log.Info(constant.NodesPostProcessCheckingReason, "No nodes need post process, skip")
		return nil
	}

	log.Info(constant.NodesPostProcessCheckingReason, "create postprocess command, nodes=%d", len(nodesWithConfig))
	cmd, err := e.createPostProcessCommand(ctx, c, bkeCluster, scheme, nodesWithConfig)
	if err != nil {
		return errors.Wrapf(err, "create postprocess command failed")
	}
	log.Info(constant.NodesPostProcessCheckingReason, "postprocess command created, command=%s", cmd.CommandName)

	log.Info(constant.NodesPostProcessCheckingReason, "wait postprocess command, command=%s", cmd.CommandName)
	err, successNodes, failedNodes := cmd.Wait()
	log.Info(constant.NodesPostProcessCheckingReason, "postprocess command done, command=%s, success=%v, failed=%v", cmd.CommandName, successNodes, failedNodes)
	if cmd.Command != nil {
		phaseutil.LogCommandInfo(*cmd.Command, log, constant.NodesPostProcessCheckingReason)
	}
	if err != nil || len(failedNodes) > 0 {
		return errors.Errorf("postprocess failed, success=%v, failed=%v", successNodes, failedNodes)
	}

	e.markPostProcessSuccess(successNodes)
	return nil
}

func (e *EnsureNodesPostProcess) markPostProcessSuccess(successNodes []string) {
	ctx, _, bkeCluster, _, log := e.Ctx.Untie()
	nodeFetcher := e.Ctx.NodeFetcher()
	for _, node := range successNodes {
		nodeIP := phaseutil.GetNodeIPFromCommandWaitResult(node)
		nodeFetcher.MarkNodeStateFlagForCluster(ctx, bkeCluster, nodeIP, bkev1beta1.NodePostProcessFlag)
		nodeFetcher.SetBKENodeStateMessage(ctx, bkeCluster.Namespace, bkeCluster.Name, nodeIP, "Post process scripts completed")
	}
	log.Info(constant.NodesPostProcessReadyReason, "postprocess success nodes=%v", successNodes)
}

func (e *EnsureNodesPostProcess) createPostProcessCommand(
	ctx context.Context,
	c client.Client,
	bkeCluster *bkev1beta1.BKECluster,
	scheme *runtime.Scheme,
	nodes bkenode.Nodes,
) (*command.Custom, error) {
	commandSpec := command.GenerateDefaultCommandSpec()
	execCommands := []agentv1beta1.ExecCommand{
		{
			ID: "execute-postprocess-scripts",
			Command: []string{
				"Postprocess",
			},
			Type:          agentv1beta1.CommandBuiltIn,
			BackoffIgnore: false,
		},
	}
	commandSpec.Commands = execCommands

	commandName := fmt.Sprintf("postprocess-all-nodes-%d", time.Now().Unix())
	customCmd := &command.Custom{
		BaseCommand: command.BaseCommand{
			Ctx:             ctx,
			Client:          c,
			NameSpace:       bkeCluster.Namespace,
			Scheme:          scheme,
			OwnerObj:        bkeCluster,
			ClusterName:     bkeCluster.Name,
			Unique:          false,
			RemoveAfterWait: true,
			WaitTimeout:     30 * time.Minute,
		},
		Nodes:        nodes,
		CommandName:  commandName,
		CommandSpec:  commandSpec,
		CommandLabel: "bke.postprocess.node",
	}

	if err := customCmd.New(); err != nil {
		return nil, errors.Wrapf(err, "create postprocess command failed")
	}
	return customCmd, nil
}

func (e *EnsureNodesPostProcess) checkPostProcessConfigExists(ctx context.Context, c client.Client, log *bkev1beta1.BKELogger, nodeIP string) bool {
	// 1. global config
	globalConfigCM := &corev1.ConfigMap{}
	globalConfigKey := client.ObjectKey{Namespace: "user-system", Name: "postprocess-all-config"}
	if err := c.Get(ctx, globalConfigKey, globalConfigCM); err == nil {
		log.Info(constant.NodesPostProcessCheckingReason, "hit postprocess global config, nodeIP=%s", nodeIP)
		return true
	}

	// 2. batch mapping
	batchMappingCM := &corev1.ConfigMap{}
	batchMappingKey := client.ObjectKey{Namespace: "user-system", Name: "postprocess-node-batch-mapping"}
	if err := c.Get(ctx, batchMappingKey, batchMappingCM); err == nil {
		mappingJSON := batchMappingCM.Data["mapping.json"]
		var mapping map[string]string
		if json.Unmarshal([]byte(mappingJSON), &mapping) == nil {
			if batchId, ok := mapping[nodeIP]; ok {
				batchConfigCM := &corev1.ConfigMap{}
				batchConfigKey := client.ObjectKey{
					Namespace: "user-system",
					Name:      fmt.Sprintf("postprocess-config-batch-%s", batchId),
				}
				if err := c.Get(ctx, batchConfigKey, batchConfigCM); err == nil {
					log.Info(constant.NodesPostProcessCheckingReason, "hit postprocess batch config %s, nodeIP=%s", batchConfigKey.Name, nodeIP)
					return true
				}
			}
		}
	}

	// 3. node config
	nodeConfigCM := &corev1.ConfigMap{}
	nodeConfigKey := client.ObjectKey{
		Namespace: "user-system",
		Name:      fmt.Sprintf("postprocess-config-node-%s", nodeIP),
	}
	if err := c.Get(ctx, nodeConfigKey, nodeConfigCM); err == nil {
		log.Info(constant.NodesPostProcessCheckingReason, "hit postprocess node config %s", nodeConfigKey.Name)
		return true
	}
	log.Info(constant.NodesPostProcessCheckingReason, "no postprocess config, nodeIP=%s", nodeIP)
	return false
}

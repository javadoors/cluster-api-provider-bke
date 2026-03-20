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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeaddon "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/addon"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	bkesource "gopkg.openfuyao.cn/cluster-api-provider-bke/common/source"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/command"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clusterutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/condition"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/scriptshelper"
)

const (
	EnsureNodesEnvName confv1beta1.BKEClusterPhase = "EnsureNodesEnv"
)

var (
	defaultEnvExtraExecScripts = []string{
		"install-lxcfs.sh",
		"install-nfsutils.sh",
		"install-etcdctl.sh",
		"install-helm.sh",
		"install-calicoctl.sh",
		"update-runc.sh",
		"clean-docker-images.py",
	}

	commonEnvExtraExecScripts = []string{
		"file-downloader.sh",
		"package-downloader.sh",
	}
)

type EnsureNodesEnv struct {
	phaseframe.BasePhase
	nodes bkenode.Nodes
}

func NewEnsureNodesEnv(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureNodesEnvName)
	return &EnsureNodesEnv{BasePhase: base}
}

func (e *EnsureNodesEnv) Execute() (ctrl.Result, error) {
	return e.CheckOrInitNodesEnv()
}

func (e *EnsureNodesEnv) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}

	// Use NodeFetcher to get nodes that need env initialization
	nodeFetcher := e.Ctx.NodeFetcher()
	bkeNodes, err := nodeFetcher.GetBKENodesWrapperForCluster(e.Ctx, new)
	if err != nil {
		return false
	}

	needExecute := phaseutil.HasNodesNeedingPhase(bkeNodes, bkev1beta1.NodeEnvFlag)
	if needExecute {
		e.SetStatus(bkev1beta1.PhaseWaiting)
	}
	return needExecute
}

func (e *EnsureNodesEnv) getNodesToInitEnv() bkenode.Nodes {
	_, _, bkeCluster, _, log := e.Ctx.Untie()
	var exceptEnvNodes bkenode.Nodes

	nodeFetcher := e.Ctx.NodeFetcher()
	bkeNodes, err := nodeFetcher.GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
	if err != nil {
		return exceptEnvNodes
	}

	log.Info("CheckAndInitNodeEnv", "GetNeedInitEnvNodes total=%d", len(bkeNodes))
	for _, bn := range bkeNodes {
		// Skip nodes that are failed, deleting, or need skip
		if bn.Status.StateCode&bkev1beta1.NodeFailedFlag != 0 ||
			bn.Status.StateCode&bkev1beta1.NodeDeletingFlag != 0 ||
			bn.Status.NeedSkip {
			continue
		}
		// Skip nodes that already have env initialized
		if bn.Status.StateCode&bkev1beta1.NodeEnvFlag != 0 {
			continue
		}
		// Skip nodes where agent is not ready
		if bn.Status.StateCode&bkev1beta1.NodeAgentReadyFlag == 0 {
			continue
		}

		node := bn.ToNode()
		exceptEnvNodes = append(exceptEnvNodes, node)
		nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeInitializing, "Initializing node env")
	}

	log.Info("CheckAndInitNodeEnv", "exceptEnvNodes total=%d", exceptEnvNodes.Length())
	return exceptEnvNodes
}

func (e *EnsureNodesEnv) setupClusterConditionAndSync() error {
	_, c, bkeCluster, _, log := e.Ctx.Untie()
	condition.ConditionMark(bkeCluster, bkev1beta1.NodesEnvCondition, confv1beta1.ConditionFalse, constant.NodesEnvNotReadyReason, "")
	if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
		log.Error(constant.NodesEnvNotReadyReason, "Failed to sync status: %v", err)
		return err
	}
	return nil
}

// BuildCommonEnvCommandParams 包含 BuildCommonEnvCommand 函数的参数
type BuildCommonEnvCommandParams struct {
	Ctx            context.Context
	Client         client.Client
	BKECluster     *bkev1beta1.BKECluster
	Scheme         *runtime.Scheme
	ExceptEnvNodes bkenode.Nodes
	Extra          []string
	ExtraHosts     []string
	DryRun         bool
	DeepRestore    bool
	Log            *bkev1beta1.BKELogger
}

// BuildCommonEnvCommand creates a common ENV command structure
func BuildCommonEnvCommand(params BuildCommonEnvCommandParams) (*command.ENV, error) {
	timeOut, err := phaseutil.GetBootTimeOut(params.BKECluster)
	if err != nil {
		params.Log.Warn(constant.NodesEnvNotReadyReason, "Get boot timeout failed. err: %v", err)
	}

	envCmd := &command.ENV{
		BaseCommand: command.BaseCommand{
			Ctx:             params.Ctx,
			NameSpace:       params.BKECluster.Namespace,
			Client:          params.Client,
			Scheme:          params.Scheme,
			OwnerObj:        params.BKECluster,
			ClusterName:     params.BKECluster.Name,
			Unique:          true,
			RemoveAfterWait: true,
			WaitTimeout:     timeOut,
		},
		Nodes:         params.ExceptEnvNodes,
		BkeConfigName: params.BKECluster.Name,
		Extra:         params.Extra,
		ExtraHosts:    params.ExtraHosts,
		DryRun:        params.DryRun,
		DeepRestore:   params.DeepRestore,
	}

	return envCmd, nil
}

func (e *EnsureNodesEnv) buildEnvCommand(exceptEnvNodes bkenode.Nodes) (*command.ENV, error) {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()
	extra, extraHosts := e.getExtraAndExtraHosts(bkeCluster)

	deepRestore := e.shouldUseDeepRestore(bkeCluster)

	envCmd, err := BuildCommonEnvCommand(BuildCommonEnvCommandParams{
		Ctx:            ctx,
		Client:         c,
		BKECluster:     bkeCluster,
		Scheme:         scheme,
		ExceptEnvNodes: exceptEnvNodes,
		Extra:          extra,
		ExtraHosts:     extraHosts,
		DryRun:         bkeCluster.Spec.DryRun,
		DeepRestore:    deepRestore,
		Log:            log,
	})
	if err != nil {
		return nil, err
	}

	if err := envCmd.New(); err != nil {
		errInfo := fmt.Sprintf("failed to create k8s env init command: %v", err)
		log.Error(constant.CommandCreateFailedReason, errInfo)
		return nil, err
	}

	return envCmd, nil
}

// shouldUseDeepRestore 判断是否启用 deep restore
func (e *EnsureNodesEnv) shouldUseDeepRestore(bkeCluster *bkev1beta1.BKECluster) bool {
	v, ok := annotation.HasAnnotation(bkeCluster, annotation.DeepRestoreNodeAnnotationKey)
	return (ok && v == "true") || !ok
}

func (e *EnsureNodesEnv) getExtraAndExtraHosts(bkeCluster *bkev1beta1.BKECluster) ([]string, []string) {
	var extra []string
	var extraHosts []string

	ep := bkeCluster.Spec.ControlPlaneEndpoint
	nodes, _ := e.Ctx.NodeFetcher().GetNodesForBKECluster(e.Ctx, bkeCluster)

	// Check if this is an HA cluster (VIP is not a node IP)
	isHAVIP := clusterutil.AvailableLoadBalancerEndPoint(ep, nodes)
	if isHAVIP {
		extra = append(extra, ep.Host)
	}

	// Add master.bocloud.com hosts mapping for any valid ControlPlaneEndpoint
	// This ensures consistency with kubeconfig generation which uses master.bocloud.com domain
	// for HA clusters. The kubeconfig uses IsHACluster() check which only requires endpoint.IsValid().
	if ep.IsValid() {
		// For HA cluster with VIP, map master.bocloud.com to VIP
		// For single master cluster with endpoint set, map master.bocloud.com to that IP
		targetIP := ep.Host
		if !isHAVIP && len(nodes.Master()) > 0 {
			// If endpoint host is a node IP (not VIP), use it directly
			targetIP = nodes.Master()[0].IP
		}
		extraHosts = append(extraHosts, fmt.Sprintf("%s:%s", constant.MasterHADomain, targetIP))
	}

	if ingressVip, _ := clusterutil.GetIngressConfig(bkeCluster.Spec.ClusterConfig.Addons); ingressVip != "" && ingressVip != ep.Host {
		extra = append(extra, ingressVip)
	}

	return extra, extraHosts
}

func (e *EnsureNodesEnv) executeEnvCommand(envCmd *command.ENV) (error, []string, []string) {
	_, _, _, _, log := e.Ctx.Untie()
	log.Info(constant.NodesEnvCheckingReason, "Waiting for the env check to complete")
	return envCmd.Wait()
}

func (e *EnsureNodesEnv) handleSuccessNodes(successNodes []string) {
	_, _, bkeCluster, _, log := e.Ctx.Untie()
	nodeFetcher := e.Ctx.NodeFetcher()

	// Get all nodes from cluster to find success node details
	allNodes, _ := nodeFetcher.GetNodesForBKECluster(e.Ctx, bkeCluster)

	var newNodes bkenode.Nodes
	for _, node := range successNodes {
		nodeIP := phaseutil.GetNodeIPFromCommandWaitResult(node)
		nodeFetcher.MarkNodeStateFlagForCluster(e.Ctx, bkeCluster, nodeIP, bkev1beta1.NodeEnvFlag)
		nodeFetcher.SetBKENodeStateMessage(e.Ctx, bkeCluster.Namespace, bkeCluster.Name, nodeIP, "Nodes env is ready")

		// Find and append the node from allNodes
		filtered := allNodes.Filter(bkenode.FilterOptions{"IP": nodeIP})
		if len(filtered) > 0 {
			newNodes = append(newNodes, filtered[0])
		}
	}

	e.nodes = newNodes
	log.Info(constant.NodesEnvCheckingReason, "handleSuccessNodes finished, newNodes=%d", e.nodes.Length())
}

func (e *EnsureNodesEnv) handleFailedNodes(envCmd *command.ENV, failedNodes []string) error {
	_, _, bkeCluster, _, log := e.Ctx.Untie()
	nodeFetcher := e.Ctx.NodeFetcher()
	for _, node := range failedNodes {
		nodeIP := phaseutil.GetNodeIPFromCommandWaitResult(node)
		nodeFetcher.SetNodeStateWithMessageForCluster(e.Ctx, bkeCluster, nodeIP, bkev1beta1.NodeInitFailed, "Failed to check k8s env")
		nodeFetcher.SetSkipNodeErrorForWorker(e.Ctx, bkeCluster.Namespace, bkeCluster.Name, nodeIP)
	}

	commandErrs, err := phaseutil.LogCommandFailed(*envCmd.Command, failedNodes, log, constant.NodesEnvNotReadyReason)
	phaseutil.MarkNodeStatusByCommandErrs(e.Ctx, e.Ctx.Client, bkeCluster, commandErrs)

	if len(failedNodes) > 0 {
		log.Error(constant.NodesEnvNotReadyReason, "failed to check k8s env in following nodes: %v", failedNodes)
	}

	return err
}

func (e *EnsureNodesEnv) finalDecisionAndCleanup(successNodes, failedNodes []string) (ctrl.Result, error) {
	_, c, bkeCluster, _, log := e.Ctx.Untie()
	if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
		return ctrl.Result{}, err
	}

	if len(successNodes) == 0 {
		errMsg := fmt.Sprintf("failed to check k8s env in all nodes: %v", failedNodes)
		log.Error(constant.NodesEnvNotReadyReason, errMsg)
		return ctrl.Result{}, errors.New(errMsg)
	}

	e.initClusterExtra()

	// 新增前置处理
	log.Info(constant.NodesEnvNotReadyReason, "start to execute user own scipts...")
	err := e.executeNodePreprocessScripts()
	if err != nil {
		return ctrl.Result{}, err
	}

	if bkeCluster.Status.ClusterHealthState == bkev1beta1.Deploying && len(failedNodes) > 0 {
		needCountFailed := phaseutil.GetNotSkipFailedNode(bkeCluster, failedNodes)
		if needCountFailed > 0 {
			errMsg := fmt.Sprintf("At Deploying state, not skip nodes need init env success, retry later. failed count: %d", needCountFailed)
			log.Info(constant.NodesEnvUpdatingReason, errMsg)
			return ctrl.Result{}, errors.New(errMsg)
		}
	}

	log.Info(constant.NodesEnvCheckingReason, "The env check is complete")
	condition.ConditionMark(bkeCluster, bkev1beta1.NodesEnvCondition, confv1beta1.ConditionTrue, constant.NodesEnvReadyReason, "")
	return ctrl.Result{}, nil
}

func (e *EnsureNodesEnv) CheckOrInitNodesEnv() (ctrl.Result, error) {
	_, _, _, _, log := e.Ctx.Untie()
	log.Info("CheckAndInitNodeEnv", "Start check and init node env for k8s")

	exceptEnvNodes := e.getNodesToInitEnv()
	if exceptEnvNodes.Length() == 0 {
		log.Info("CheckAndInitNodeEnv", "No more node need to init env")
		return ctrl.Result{}, nil
	}
	// 缓存本次需要初始化环境的节点，供后续前置处理使用
	e.nodes = exceptEnvNodes
	log.Info("CheckAndInitNodeEnv", "cache e.nodes=%d", e.nodes.Length())

	if err := e.setupClusterConditionAndSync(); err != nil {
		return ctrl.Result{}, err
	}

	envCmd, err := e.buildEnvCommand(exceptEnvNodes)
	if err != nil {
		return ctrl.Result{}, err
	}

	err, successNodes, failedNodes := e.executeEnvCommand(envCmd)
	if err != nil {
		errInfo := fmt.Sprintf("failed to check k8s env: %v", err)
		log.Error(constant.NodesEnvNotReadyReason, errInfo)
		return ctrl.Result{}, err
	}

	e.handleSuccessNodes(successNodes)

	if handleErr := e.handleFailedNodes(envCmd, failedNodes); handleErr != nil {
		errInfo := fmt.Sprintf("handle failed nodes failed: %v", handleErr)
		log.Error(constant.NodesEnvNotReadyReason, errInfo)
	}

	return e.finalDecisionAndCleanup(successNodes, failedNodes)
}

func (e *EnsureNodesEnv) initClusterExtra() {
	ctx, _, bkeCluster, _, log := e.Ctx.Untie()

	localClient, err := kube.NewClientFromRestConfig(ctx, e.Ctx.RestConfig)
	if err != nil {
		log.Warn(constant.EnvExtraExecScriptFailed, "获取k8s client 失败, 跳过自定义脚本执行, err: %v", err)
		return
	}

	scriptsLi, err := scriptshelper.ListScriptsConfigMaps(e.Ctx.Client)
	if err != nil {
		log.Warn(constant.InternalErrorReason, "获取环境初始化自定义脚本 configmaps 失败, 跳过自定义脚本执行, err: %v", err)
		return
	}
	cfg := bkeinit.BkeConfig(*bkeCluster.Spec.ClusterConfig)

	// 安装common scripts
	installParams := InstallScriptParams{
		LocalClient: localClient,
		BKECluster:  bkeCluster,
		Log:         log,
		ScriptsLi:   scriptsLi,
	}
	e.installCommonScripts(installParams)

	// 安装其他自定义脚本
	otherInstallParams := InstallOtherScriptParams{
		LocalClient: localClient,
		BKECluster:  bkeCluster,
		Log:         log,
		ScriptsLi:   scriptsLi,
		Cfg:         cfg,
	}
	e.installOtherCustomScripts(otherInstallParams)
}

// InstallScriptParams 包含安装脚本所需的参数
type InstallScriptParams struct {
	LocalClient kube.RemoteKubeClient
	BKECluster  *bkev1beta1.BKECluster
	Log         *bkev1beta1.BKELogger
	ScriptsLi   []string
}

// InstallOtherScriptParams 包含安装其他脚本所需的参数
type InstallOtherScriptParams struct {
	LocalClient kube.RemoteKubeClient
	BKECluster  *bkev1beta1.BKECluster
	Log         *bkev1beta1.BKELogger
	ScriptsLi   []string
	Cfg         bkeinit.BkeConfig
}

// installCommonScripts 安装common scripts
func (e *EnsureNodesEnv) installCommonScripts(params InstallScriptParams) {
	for _, script := range commonEnvExtraExecScripts {
		if !utils.ContainsString(params.ScriptsLi, script) {
			params.Log.Warn(constant.EnvExtraExecScriptFailed, "基础脚本 %q 不存在, 跳过自定义脚本执行", script)
			return
		}

		nodesIps, err := e.getNodesIpsByScript(script)
		if err != nil {
			params.Log.Warn(constant.EnvExtraExecScriptSkip, "获取安装基础脚本 %q 节点IP失败, 跳过自定义脚本执行, err: %v", script, err)
			return
		}
		if len(nodesIps) == 0 {
			params.Log.Warn(constant.EnvExtraExecScriptSkip, "获取安装基础脚本 %q 节点IP为空, 跳过自定义脚本执行", script)
			return
		}

		param := map[string]string{
			"nodesIps": nodesIps,
		}

		addonT := e.createAddonTransfer(script, param, false)

		if err := params.LocalClient.InstallAddon(params.BKECluster, addonT, nil, nil, e.nodes); err != nil {
			params.Log.Warn(constant.EnvExtraExecScriptFailed, "安装基础脚本 %q 失败, 跳过自定义脚本执行, err: %v", script, err)
			return
		}

		params.Log.Info(constant.EnvExtraExecScriptSuccess, "额外安装基础脚本 %q 成功", script)
	}
}

// installOtherCustomScripts 安装其他自定义脚本
func (e *EnsureNodesEnv) installOtherCustomScripts(params InstallOtherScriptParams) {
	// 执行其他脚本
	otherCustomScripts := defaultEnvExtraExecScripts

	scriptsScope, ok := params.Cfg.CustomExtra["envExtraExecScripts"]
	if ok {
		otherCustomScripts = strings.Split(scriptsScope, ",")
	}

	httpRepo := bkesource.GetCustomDownloadPath(params.Cfg.YumRepo())

	for _, script := range otherCustomScripts {
		if !utils.ContainsString(params.ScriptsLi, script) {
			params.Log.Warn(constant.EnvExtraExecScriptSkip, "自定义脚本 %q 不存在, 跳过自定义脚本执行", script)
			continue
		}

		if script == "update-runc.sh" && params.Cfg.Cluster.ContainerRuntime.CRI == bkeinit.CRIContainerd {
			params.Log.Info(constant.EnvExtraExecScriptSkip, "自定义脚本 %q 不支持 containerd, 跳过自定义脚本执行", script)
			continue
		}

		nodesIps, err := e.getNodesIpsByScript(script)
		if err != nil {
			params.Log.Warn(constant.EnvExtraExecScriptSkip, "获取自定义脚本 %q 节点IP失败, 跳过自定义脚本执行, err: %v", script, err)
			continue
		}
		if len(nodesIps) == 0 {
			params.Log.Warn(constant.EnvExtraExecScriptSkip, "获取自定义脚本 %q 节点IP为空, 跳过自定义脚本执行", script)
			continue
		}

		param := map[string]string{
			"nodesIps": nodesIps,
			"httpRepo": httpRepo,
		}

		block := script == "update-runc.sh"
		addonT := e.createAddonTransfer(script, param, block)

		if err := params.LocalClient.InstallAddon(params.BKECluster, addonT, nil, nil, e.nodes); err != nil {
			params.Log.Warn(constant.EnvExtraExecScriptFailed, "执行自定义脚本 %q 失败, 跳过自定义脚本执行, err: %v", script, err)
			continue
		}

		params.Log.Info(constant.EnvExtraExecScriptSuccess, "额外执行自定义脚本 %q 成功", script)
	}
}

// executeNodePreprocessScripts 执行节点前置处理脚本
func (e *EnsureNodesEnv) executeNodePreprocessScripts() error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()

	// 1. 获取所有业务节点
	nodes := e.nodes
	log.Info(constant.NodesEnvCheckingReason, "开始检查节点前置处理配置, 总节点数=%d", len(nodes))

	// 2. 收集所有有配置的节点（用于创建单个Command并下发到所有目标节点）
	var nodesWithConfig bkenode.Nodes
	for _, node := range nodes {
		// 节点IP为空时无法匹配配置与nodeSelector，直接跳过并打印日志
		if node.IP == "" {
			log.Warn(constant.NodesEnvCheckingReason, "节点IP为空，跳过前置处理配置检查")
			continue
		}
		log.Info(constant.NodesEnvCheckingReason, "开始检查节点前置处理配置, nodeIP=%s", node.IP)
		hasConfig := e.checkPreprocessConfigExists(ctx, c, log, node.IP)
		if !hasConfig {
			log.Warn(constant.NodesEnvCheckingReason, "节点 %s 没有匹配的前置处理配置，跳过", node.IP)
			continue
		}
		nodesWithConfig = append(nodesWithConfig, node)
	}

	log.Info(constant.NodesEnvCheckingReason, "前置处理配置检查完成，总节点数=%d，命中配置节点数=%d", len(nodes), len(nodesWithConfig))

	// 如果没有节点有配置，直接返回
	if len(nodesWithConfig) == 0 {
		log.Info(constant.NodesEnvCheckingReason, "没有节点需要执行前置处理，跳过")
		return nil
	}

	// 3. 创建一个Command资源，包含所有有配置的节点
	log.Info(constant.NodesEnvCheckingReason, "开始创建前置处理Command资源, nodes=%d", len(nodesWithConfig))
	cmd, err := e.createPreprocessCommand(
		ctx, c, bkeCluster, scheme, nodesWithConfig,
	)
	if err != nil {
		return errors.Wrapf(err, "创建前置处理Command资源失败")
	}
	log.Info(constant.NodesEnvCheckingReason, "前置处理Command资源创建完成, command=%s", cmd.CommandName)

	// 4. 等待Command执行完成（所有节点）
	log.Info(constant.NodesEnvCheckingReason, "开始等待前置处理Command执行完成, command=%s", cmd.CommandName)
	err, successNodes, failedNodes := cmd.Wait()
	log.Info(constant.NodesEnvCheckingReason, "前置处理Command执行完成, command=%s, successNodes=%v, failedNodes=%v", cmd.CommandName, successNodes, failedNodes)
	if cmd.Command != nil {
		phaseutil.LogCommandInfo(*cmd.Command, log, constant.NodesEnvCheckingReason)
	}
	if err != nil || len(failedNodes) > 0 {
		return errors.Errorf("前置处理执行失败，成功节点: %v, 失败节点: %v",
			successNodes, failedNodes)
	}

	return nil
}

// createPreprocessCommand 创建前置处理Command资源（包含所有节点）
func (e *EnsureNodesEnv) createPreprocessCommand(
	ctx context.Context,
	c client.Client,
	bkeCluster *bkev1beta1.BKECluster,
	scheme *runtime.Scheme,
	nodes bkenode.Nodes,
) (*command.Custom, error) {
	commandSpec := command.GenerateDefaultCommandSpec()

	// 创建执行前置处理脚本的命令
	execCommands := []agentv1beta1.ExecCommand{
		{
			ID: "execute-preprocess-scripts",
			Command: []string{
				"Preprocess", // 内置执行器名称，不传递nodeIP参数，PreprocessPlugin会自动获取当前节点IP
			},
			Type:          agentv1beta1.CommandBuiltIn,
			BackoffIgnore: false,
		},
	}

	commandSpec.Commands = execCommands

	// 创建Command资源（nodeSelector包含所有节点IP）
	commandName := fmt.Sprintf("preprocess-all-nodes-%d", time.Now().Unix())

	customCmd := &command.Custom{
		BaseCommand: command.BaseCommand{
			Ctx:             ctx,
			Client:          c, // 管理集群Client
			Scheme:          scheme,
			OwnerObj:        bkeCluster,
			ClusterName:     bkeCluster.Name,
			NameSpace:       bkeCluster.Namespace,
			Unique:          false,
			RemoveAfterWait: true,
			WaitTimeout:     30 * time.Minute,
		},
		Nodes:        nodes, // 所有有配置的节点
		CommandLabel: "bke.preprocess.node",
		CommandName:  commandName,
		CommandSpec:  commandSpec,
	}

	if err := customCmd.New(); err != nil {
		return nil, errors.Wrapf(err, "创建Command资源失败")
	}

	return customCmd, nil
}

// checkPreprocessConfigExists 检查前置处理配置是否存在（按优先级：全局 > 批次 > 节点）
// 增加详细日志，便于定位“找不到需要执行节点”的原因
func (e *EnsureNodesEnv) checkPreprocessConfigExists(ctx context.Context, c client.Client, log *bkev1beta1.BKELogger, nodeIP string) bool {
	// 1. 优先检查全局配置
	globalConfigCM := &corev1.ConfigMap{}
	globalConfigKey := client.ObjectKey{
		Namespace: "user-system",
		Name:      "preprocess-all-config",
	}
	if err := c.Get(ctx, globalConfigKey, globalConfigCM); err == nil {
		log.Info(constant.NodesEnvCheckingReason, "命中全局配置 preprocess-all-config, nodeIP=%s", nodeIP)
		return true // 全局配置存在，直接返回
	}
	log.Info(constant.NodesEnvCheckingReason, "未找到全局配置 preprocess-all-config, nodeIP=%s", nodeIP)

	// 2. 全局配置不存在，检查批次配置
	batchMappingCM := &corev1.ConfigMap{}
	batchMappingKey := client.ObjectKey{
		Namespace: "user-system",
		Name:      "preprocess-node-batch-mapping",
	}
	if err := c.Get(ctx, batchMappingKey, batchMappingCM); err == nil {
		// 解析批次映射
		mappingJSON := batchMappingCM.Data["mapping.json"]
		var mapping map[string]string
		if json.Unmarshal([]byte(mappingJSON), &mapping) == nil {
			if batchId, ok := mapping[nodeIP]; ok {
				batchConfigCM := &corev1.ConfigMap{}
				batchConfigKey := client.ObjectKey{
					Namespace: "user-system",
					Name:      fmt.Sprintf("preprocess-config-batch-%s", batchId),
				}
				if err := c.Get(ctx, batchConfigKey, batchConfigCM); err == nil {
					log.Info(constant.NodesEnvCheckingReason, "命中批次配置 %s, nodeIP=%s", batchConfigKey.Name, nodeIP)
					return true // 批次配置存在，直接返回
				}
				log.Info(constant.NodesEnvCheckingReason, "未找到批次配置 %s, nodeIP=%s", batchConfigKey.Name, nodeIP)
			} else {
				log.Info(constant.NodesEnvCheckingReason, "批次映射中未找到节点, nodeIP=%s", nodeIP)
			}
		} else {
			log.Warn(constant.NodesEnvCheckingReason, "批次映射解析失败, nodeIP=%s", nodeIP)
		}
	} else {
		log.Info(constant.NodesEnvCheckingReason, "未找到批次映射 preprocess-node-batch-mapping, nodeIP=%s", nodeIP)
	}

	// 3. 全局和批次配置都不存在，检查节点配置
	nodeConfigCM := &corev1.ConfigMap{}
	nodeConfigKey := client.ObjectKey{
		Namespace: "user-system",
		Name:      fmt.Sprintf("preprocess-config-node-%s", nodeIP),
	}
	if err := c.Get(ctx, nodeConfigKey, nodeConfigCM); err == nil {
		log.Info(constant.NodesEnvCheckingReason, "命中节点配置 %s", nodeConfigKey.Name)
		return true // 节点配置存在
	}
	log.Info(constant.NodesEnvCheckingReason, "未找到节点配置 %s", nodeConfigKey.Name)

	return false // 三种配置都不存在
}

// getNodesIpsByScript returns the node IPs based on the script name
func (e *EnsureNodesEnv) getNodesIpsByScript(script string) (string, error) {
	masterNodes := e.nodes.Master()
	etcdNodes := e.nodes.Etcd()

	allNodesIps := make([]string, e.nodes.Length())
	etcdNodesIps := make([]string, etcdNodes.Length())
	masterNodesIps := make([]string, masterNodes.Length())
	for i, node := range e.nodes {
		// Check bounds before assigning to slice
		if i >= 0 && i < len(allNodesIps) {
			allNodesIps[i] = node.IP
		}
	}
	for i, node := range etcdNodes {
		// Check bounds before assigning to slice
		if i >= 0 && i < len(etcdNodesIps) {
			etcdNodesIps[i] = node.IP
		}
	}
	for i, node := range masterNodes {
		// Check bounds before assigning to slice
		if i >= 0 && i < len(masterNodesIps) {
			masterNodesIps[i] = node.IP
		}
	}

	// Handle different scripts with dedicated functions to reduce cyclomatic complexity
	switch script {
	case "file-downloader.sh":
		return e.handleFileDownloaderScript(allNodesIps)
	case "package-downloader.sh":
		return e.handlePackageDownloaderScript(allNodesIps)
	case "install-lxcfs.sh":
		return e.handleInstallLxcfsScript(allNodesIps)
	case "install-nfsutils.sh":
		return e.handleInstallNfsutilsScript()
	case "install-etcdctl.sh":
		return e.handleInstallEtcdctlScript(etcdNodesIps)
	case "install-helm.sh":
		return e.handleInstallHelmScript(masterNodesIps)
	case "install-calicoctl.sh":
		return e.handleInstallCalicoctlScript(masterNodesIps)
	case "update-runc.sh":
		return e.handleUpdateRuncScript(allNodesIps)
	case "clean-docker-images.py":
		return e.handleCleanDockerImagesScript()
	default:
		return e.handleDefaultScript(allNodesIps)
	}
}

// handleFileDownloaderScript handles the file-downloader.sh script
func (e *EnsureNodesEnv) handleFileDownloaderScript(allNodesIps []string) (string, error) {
	return strings.Join(allNodesIps, ","), nil
}

// handlePackageDownloaderScript handles the package-downloader.sh script
func (e *EnsureNodesEnv) handlePackageDownloaderScript(allNodesIps []string) (string, error) {
	return strings.Join(allNodesIps, ","), nil
}

// handleInstallLxcfsScript handles the install-lxcfs.sh script
func (e *EnsureNodesEnv) handleInstallLxcfsScript(allNodesIps []string) (string, error) {
	return strings.Join(allNodesIps, ","), nil
}

// handleInstallNfsutilsScript handles the install-nfsutils.sh script
func (e *EnsureNodesEnv) handleInstallNfsutilsScript() (string, error) {
	if v, ok := e.Ctx.BKECluster.Spec.ClusterConfig.CustomExtra["pipelineServer"]; ok {
		return v, nil
	}
	return "", errors.Errorf("没有在 Spec.ClusterConfig.CustomExtra 中配置 pipelineServer")
}

// handleInstallEtcdctlScript handles the install-etcdctl.sh script
func (e *EnsureNodesEnv) handleInstallEtcdctlScript(etcdNodesIps []string) (string, error) {
	return strings.Join(etcdNodesIps, ","), nil
}

// handleInstallHelmScript handles the install-helm.sh script
func (e *EnsureNodesEnv) handleInstallHelmScript(masterNodesIps []string) (string, error) {
	return strings.Join(masterNodesIps, ","), nil
}

// handleInstallCalicoctlScript handles the install-calicoctl.sh script
func (e *EnsureNodesEnv) handleInstallCalicoctlScript(masterNodesIps []string) (string, error) {
	return strings.Join(masterNodesIps, ","), nil
}

// handleUpdateRuncScript handles the update-runc.sh script
func (e *EnsureNodesEnv) handleUpdateRuncScript(allNodesIps []string) (string, error) {
	if v, ok := e.Ctx.BKECluster.Spec.ClusterConfig.CustomExtra["host"]; ok && v != "" {
		updateRuncNodesIps := make([]string, 0)
		for _, node := range allNodesIps {
			if node == v {
				continue
			}
			updateRuncNodesIps = append(updateRuncNodesIps, node)
		}
		return strings.Join(updateRuncNodesIps, ","), nil
	}

	return strings.Join(allNodesIps, ","), nil
}

// handleCleanDockerImagesScript handles the clean-docker-images.py script
func (e *EnsureNodesEnv) handleCleanDockerImagesScript() (string, error) {
	nodes := ""
	if v, ok := e.Ctx.BKECluster.Spec.ClusterConfig.CustomExtra["pipelineServer"]; ok {
		nodes = v
	} else {
		return "", errors.Errorf("没有在 Spec.ClusterConfig.CustomExtra 中配置 pipelineServer")
	}

	if v, ok := e.Ctx.BKECluster.Spec.ClusterConfig.CustomExtra["pipelineServerEnableCleanImages"]; ok && v == "true" {
		return nodes, nil
	}

	return "", errors.Errorf("没有在 Spec.ClusterConfig.CustomExtra 中配置 pipelineServerEnableCleanImages")
}

// handleDefaultScript handles any other scripts not specifically defined
func (e *EnsureNodesEnv) handleDefaultScript(allNodesIps []string) (string, error) {
	return strings.Join(allNodesIps, ","), nil
}

// createAddonTransfer 创建AddonTransfer对象
func (e *EnsureNodesEnv) createAddonTransfer(script string, param map[string]string, block bool) *bkeaddon.AddonTransfer {
	return &bkeaddon.AddonTransfer{
		Addon: &confv1beta1.Product{
			Name:    "clusterextra",
			Version: script,
			Param:   param,
			Block:   block,
		},
		Operate: bkeaddon.CreateAddon,
	}
}

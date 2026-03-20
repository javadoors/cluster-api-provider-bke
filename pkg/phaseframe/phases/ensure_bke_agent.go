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
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	bkevalidate "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/validation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/certs"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	bkessh "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/remote"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clusterutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/condition"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
)

const (
	EnsureBKEAgentName confv1beta1.BKEClusterPhase = "EnsureBKEAgent"
	// deployCertDir 是远端 registry 的证书目录
	deployCertDir = "/etc/openFuyao/certs"
	// deployCACrt is the certification chain path for saving
	deployCACrt = deployCertDir + "/trust-chain.crt"
	// certConfigDir  is the certification config path for saving
	certConfigDir = deployCertDir + "/cert_config"
	// ServiceFilePermission is the file permission for service files
	ServiceFilePermission = 0644
)

type EnsureBKEAgent struct {
	phaseframe.BasePhase
	localKubeConfig []byte
	needPushNodes   bkenode.Nodes
}

func NewEnsureBKEAgent(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureBKEAgentName)
	return &EnsureBKEAgent{BasePhase: base}
}

func (e *EnsureBKEAgent) Execute() (_ ctrl.Result, err error) {
	_, _, _, _, log := e.Ctx.Untie()

	if err := e.loadLocalKubeConfig(); err != nil {
		log.Error(constant.BKEAgentNotReadyReason, "Failed to load local kube config, err: %v", err)
		return ctrl.Result{}, err
	}

	// get need push agent nodes
	if err := e.getNeedPushNodes(); err != nil {
		log.Error(constant.BKEAgentNotReadyReason, "Failed to get need push nodes, err: %v", err)
		return ctrl.Result{}, err
	}

	if e.needPushNodes == nil || len(e.needPushNodes) == 0 {
		log.Info(constant.BKEAgentNotReadyReason, "No more nodes need to push BKEAgent")
		return ctrl.Result{}, nil
	}

	// start push
	log.Info(constant.BKEAgentNotReadyReason, "Push BKEAgent will take some time, please wait")

	if err := e.pushAgent(); err != nil {
		log.Warn(constant.BKEAgentNotReadyReason, "Failed to push agent, err: %v", err)
		return ctrl.Result{}, err
	}

	log.Info(constant.BKEAgentNotReadyReason, "Collect node hostname if it is not set in the BKECluster resource")
	if err := e.pingAgent(); err != nil {
		log.Warn(constant.BKEAgentNotReadyReason, "Failed to ping agent, err: %v", err)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (e *EnsureBKEAgent) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}

	// Use NodeFetcher to get nodes that need agent push
	nodeFetcher := e.Ctx.NodeFetcher()
	bkeNodes, err := nodeFetcher.GetBKENodesWrapperForCluster(e.Ctx, new)
	if err != nil {
		return false
	}

	needExecute := phaseutil.HasNodesNeedingPhase(bkeNodes, bkev1beta1.NodeAgentPushedFlag)
	if needExecute {
		e.SetStatus(bkev1beta1.PhaseWaiting)
	}
	return needExecute
}

func (e *EnsureBKEAgent) loadLocalKubeConfig() error {
	ctx, c, bkeCluster, _, log := e.Ctx.Untie()

	hasClusterAPI := false
	if bkeCluster != nil && bkeCluster.Spec.ClusterConfig != nil && bkeCluster.Spec.ClusterConfig.Addons != nil {
		for _, addon := range bkeCluster.Spec.ClusterConfig.Addons {
			if addon.Name == "cluster-api" {
				hasClusterAPI = true
				break
			}
		}
	}

	var localKubeConfig []byte
	var err error

	if !hasClusterAPI {
		localKubeConfig, err = phaseutil.GetLeastPrivilegeKubeConfig(ctx, c)
		if err != nil {
			log.Warn(constant.BKEAgentNotReadyReason, "Failed to get least privilege kubeconfig, fallback to local kubeconfig, err：%v", err)
			// 回退到使用 localKubeConfig，不需要创建 RBAC
			localKubeConfig, err = phaseutil.GetLocalKubeConfig(ctx, c)
			if err != nil {
				log.Error(constant.BKEAgentNotReadyReason, "Failed to get local kubeconfig after fallback, err：%v", err)
				return errors.Wrap(err, "failed to get local kubeconfig after fallback")
			}
		} else {
			// GetLeastPrivilegeKubeConfig 成功，需要创建 RBAC
			localKubeConfigBytes, err := phaseutil.GetLocalKubeConfig(ctx, c)
			if err != nil {
				log.Error(constant.BKEAgentNotReadyReason, "Failed to get localkubeconfig for RBAC creation, err：%v", err)
				return errors.Wrap(err, "failed to get localkubeconfig for RBAC creation")
			}

			if err := phaseutil.CreateBKEAgentRBACWithLocalKubeConfig(ctx, localKubeConfigBytes, bkeCluster); err != nil {
				log.Warn(constant.BKEAgentNotReadyReason, "Failed to create RBAC resources, err：%v", err)
				return errors.Wrap(err, "failed to create RBAC resources")
			}
		}
	} else {
		localKubeConfig, err = phaseutil.GetLocalKubeConfig(ctx, c)
		if err != nil {
			log.Error(constant.BKEAgentNotReadyReason, "Failed to get local kubeconfig, err：%v", err)
			return errors.Wrap(err, "failed to get local kubeconfig")
		}
	}

	e.localKubeConfig = localKubeConfig
	return nil
}

func (e *EnsureBKEAgent) getNeedPushNodes() error {
	// Use NodeFetcher to get nodes from API server (not from local kubeconfig file)
	nodeFetcher := e.Ctx.NodeFetcher()
	bkeNodes, err := nodeFetcher.GetBKENodesWrapperForCluster(e.Ctx, e.Ctx.BKECluster)
	if err != nil {
		return errors.Wrap(err, "failed to get BKENodes from cluster")
	}

	// Use the new function that accepts pre-fetched BKENodes
	nodes := phaseutil.GetNeedPushAgentNodesWithBKENodes(e.Ctx.BKECluster, bkeNodes)
	if len(nodes) == 0 {
		return nil
	}
	// set node state
	for _, node := range nodes {
		if err := e.Ctx.SetNodeStateWithMessage(node.IP, bkev1beta1.NodeInitializing, "Pushing bkeagent"); err != nil {
			e.Ctx.Log.Warn("Failed to set node state for %s: %v", node.IP, err)
		}
	}
	if err := mergecluster.SyncStatusUntilComplete(e.Ctx.Client, e.Ctx.BKECluster); err != nil {
		return err
	}
	e.needPushNodes = nodes
	return nil
}

// pushAgent push bkeagent to Nodes
func (e *EnsureBKEAgent) pushAgent() error {
	ctx, c, bkeCluster, _, _ := e.Ctx.Untie()

	// Log the nodes that will receive the agent
	e.logPushAgentStart()

	// Prepare the service file
	servicePath, err := e.prepareServiceFile(bkeCluster)
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(filepath.Dir(servicePath)); err != nil {
			e.Ctx.Log.Warn("Failed to remove temporary directory: %v", err.Error())
		}
	}()

	// Push the agent to nodes
	failedNodeIPs, err := e.performAgentPush(ctx, c, bkeCluster, servicePath)
	if err != nil {
		return err
	}

	// Process the results and handle errors
	return e.handlePushResults(ctx, c, bkeCluster, failedNodeIPs)
}

// logPushAgentStart logs the start of the agent push process
func (e *EnsureBKEAgent) logPushAgentStart() {
	var nodesInfoToPrint []string
	for _, node := range e.needPushNodes {
		nodesInfoToPrint = append(nodesInfoToPrint, phaseutil.NodeInfo(node))
	}
	e.Ctx.Log.Info(constant.BKEAgentNotReadyReason, "Start push BKEAgent to node(s) %v", nodesInfoToPrint)
}

// prepareServiceFile prepares the bkeagent service file
func (e *EnsureBKEAgent) prepareServiceFile(bkeCluster *bkev1beta1.BKECluster) (string, error) {
	// generate bkeagent.service
	dirName, err := os.MkdirTemp(os.TempDir(), e.Ctx.BKECluster.Name)
	if err != nil {
		return "", errors.Errorf("Failed to create temp dir, err: %v", err)
	}

	file, err := os.ReadFile("/bkeagent.service.tmpl")
	if err != nil {
		if removeErr := os.RemoveAll(dirName); removeErr != nil {
			e.Ctx.Log.Warn("Failed to remove temporary directory: %v", removeErr.Error())
		}
		return "", errors.Errorf("Failed to read /bkeagent.service.tmpl, err: %v", err)
	}

	ntpServer := strings.ReplaceAll(string(file), "--ntpserver=", fmt.Sprintf("--ntpserver=%s", bkeCluster.Spec.ClusterConfig.Cluster.NTPServer))
	healthPort := strings.ReplaceAll(ntpServer, "--health-port=", fmt.Sprintf("--health-port=%s", bkeCluster.Spec.ClusterConfig.Cluster.AgentHealthPort))

	servicePath := filepath.Join(dirName, "bkeagent.service")
	if err = os.WriteFile(servicePath, []byte(healthPort), ServiceFilePermission); err != nil {
		if removeErr := os.RemoveAll(dirName); removeErr != nil {
			e.Ctx.Log.Warn("Failed to remove temporary directory: %v", removeErr.Error())
		}
		return "", errors.Errorf("Failed to create bkeagent.service, err: %v", err)
	}

	return servicePath, nil
}

// performAgentPush performs the actual agent push operation
func (e *EnsureBKEAgent) performAgentPush(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, servicePath string) ([]string, error) {
	hosts := phaseutil.NodeToRemoteHost(e.needPushNodes)

	var failedNodeIPs []string
	failedNodesWithErr, err := e.sshPushAgent(ctx, hosts, e.localKubeConfig, servicePath)
	for nodeIP, errInfos := range failedNodesWithErr {
		failedNodeIPs = append(failedNodeIPs, nodeIP)
		if setErr := e.Ctx.SetNodeStateWithMessage(nodeIP, bkev1beta1.NodeInitFailed, fmt.Sprintf("Failed push bkeagent, err: %v", errInfos)); setErr != nil {
			e.Ctx.Log.Warn("Failed to set node state for %s: %v", nodeIP, setErr)
		}
		if err := e.Ctx.NodeFetcher().SetNodeNeedSkip(ctx, bkeCluster.Namespace, bkeCluster.Name, nodeIP, true); err != nil {
			e.Ctx.Log.Warn("Failed to set skip node error for %s: %v", nodeIP, err)
		}
		e.Ctx.Log.Error(constant.BKEAgentNotReadyReason, errInfos.Error())
	}
	return failedNodeIPs, err
}

// handlePushResults processes the results of the agent push operation
func (e *EnsureBKEAgent) handlePushResults(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, failedNodeIPs []string) error {
	// 没有一个节点成功返回
	if len(failedNodeIPs) == e.needPushNodes.Length() {
		//及时更新node 失败状态
		if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
			return err
		}
		var nodesInfoToPrint []string
		for _, node := range e.needPushNodes {
			nodesInfoToPrint = append(nodesInfoToPrint, phaseutil.NodeInfo(node))
		}
		return errors.Errorf("Failed to push agent to nodes %v", nodesInfoToPrint)
	}

	// 给成功的节点添加标记,避免再次push
	nf := e.Ctx.NodeFetcher()
	for _, node := range e.needPushNodes {
		if utils.ContainsString(failedNodeIPs, node.IP) {
			continue
		}
		if err := nf.MarkNodeStateFlagForCluster(ctx, bkeCluster, node.IP, bkev1beta1.NodeAgentPushedFlag); err != nil {
			e.Ctx.Log.Warn(constant.InternalErrorReason, "Failed to mark node state flag for %s: %v", node.IP, err)
		}
	}

	if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
		return err
	}

	// 有master节点没有成功返回
	// todo现在是忽略了master节点加入的情况
	for _, nodeIP := range failedNodeIPs {
		if e.needPushNodes.Master().Filter(bkenode.FilterOptions{"IP": nodeIP}).Length() != 0 {
			e.Ctx.Log.Warn(constant.BKEAgentNotReadyReason, "Push agent to master node failed, process exit")
			return errors.Errorf("Push agent to master node failed, process exit")
		}
	}

	// 逻辑调整，安装过程中忽略worker节点失败的情况，继续后面的安装流程
	e.Ctx.Log.Info(constant.BKEAgentUpdatingReason, "at push bkeagent state, failed nodes: %v", failedNodeIPs)

	return nil
}

// prepareFileUploadList prepare for the certifications and configs for uploading
func (e *EnsureBKEAgent) prepareFileUploadList(servicePath string) []bkessh.File {
	fileUpList := []bkessh.File{
		{Src: servicePath, Dst: "/etc/systemd/system"},
	}
	// add certification chain to upload file list
	fileUpList = e.addFilesToUploadList(fileUpList, []string{deployCACrt}, deployCertDir)

	// add global certification and key to upload file list
	fileUpList = e.addGlobalCAFilesIfNeeded(fileUpList)

	// add certification configs to upload file list
	fileUpList = e.addCSRFilesToUploadList(fileUpList)

	return fileUpList
}

// addGlobalCAFilesIfNeeded  adds global certification and key to upload file list if global certification and key exist
func (e *EnsureBKEAgent) addGlobalCAFilesIfNeeded(fileUpList []bkessh.File) []bkessh.File {
	_, _, bkeCluster, _, _ := e.Ctx.Untie()

	if bkeCluster == nil || bkeCluster.Spec.ClusterConfig.Addons == nil || len(bkeCluster.Spec.ClusterConfig.Addons) == 0 {
		return fileUpList
	}

	hasClusterAPI := false
	for _, addon := range bkeCluster.Spec.ClusterConfig.Addons {
		if addon.Name == "cluster-api" {
			hasClusterAPI = true
			break
		}
	}

	if !hasClusterAPI {
		return fileUpList
	}

	globalCAFiles := []string{
		certs.GlobalCACertPath,
		certs.GlobalCAKeyPath,
	}
	return e.addFilesToUploadList(fileUpList, globalCAFiles, deployCertDir)
}

// addFilesToUploadList add file generic function
func (e *EnsureBKEAgent) addFilesToUploadList(fileUpList []bkessh.File, filePaths []string, dstDir string) []bkessh.File {
	for _, filePath := range filePaths {
		if _, err := os.Stat(filePath); err == nil {

			fileUpList = append(fileUpList, bkessh.File{
				Src: filePath,
				Dst: dstDir,
			})
			log.Infof("file %s exists，upload to %s", filePath, dstDir)
		} else if os.IsNotExist(err) {
			log.Infof("file %s not exists，not upload ", filePath)
		} else {
			log.Warnf("check %s err：%v，not upload ", filePath, err)
		}
	}
	return fileUpList
}

// addCSRFilesToUploadList check certification configs and add to upload file list
func (e *EnsureBKEAgent) addCSRFilesToUploadList(fileUpList []bkessh.File) []bkessh.File {
	csrFiles := []string{
		certs.ConfigKeyClusterCAPolicy,
		certs.ConfigKeyClusterCACSR,
		certs.ConfigKeySignPolicy,
		certs.ConfigKeyAPIServerCSR,
		certs.ConfigKeyAPIServerEtcdClientCSR,
		certs.ConfigKeyFrontProxyClientCSR,
		certs.ConfigKeyAPIServerKubeletClientCSR,
		certs.ConfigKeyFrontProxyCACSR,
		certs.ConfigKeyEtcdCACSR,
		certs.ConfigKeyEtcdServerCSR,
		certs.ConfigKeyEtcdHealthcheckClientCSR,
		certs.ConfigKeyEtcdPeerCSR,
		certs.ConfigKeyAdminKubeConfigCSR,
		certs.ConfigKeyKubeletKubeConfigCSR,
		certs.ConfigKeyControllerManagerCSR,
		certs.ConfigKeySchedulerCSR,
		certs.ConfigKeyKubeProxyCSR,
	}

	csrFilePaths := make([]string, 0, len(csrFiles))
	for _, csrFile := range csrFiles {
		csrFilePaths = append(csrFilePaths, filepath.Join(certConfigDir, csrFile))
	}

	return e.addFilesToUploadList(fileUpList, csrFilePaths, certConfigDir)
}

func (e *EnsureBKEAgent) sshPushAgent(ctx context.Context, hosts bkessh.Hosts, localKubeConfig []byte, servicePath string) (map[string]error, error) {

	pushAgentErrs := map[string]error{}

	multiCli := bkessh.NewMultiCli(ctx)
	defer multiCli.Close()
	multiCli.SetLogger(e.Ctx.Log.NormalLogger)

	regisErrs := multiCli.RegisterHosts(hosts)
	for hostIP, err := range regisErrs {
		pushAgentErrs[hostIP] = err
	}
	if !e.checkAvailableHosts(multiCli, pushAgentErrs) {
		return pushAgentErrs, errors.New("No available hosts to push")
	}

	// 检查命令获取目标机器系统架构
	unknownArchErrs := multiCli.RegisterHostsInfo()
	for nodeIP, err := range unknownArchErrs {
		e.Ctx.Log.Warn("HostArchUnknown", "node %s, err: %v", nodeIP, "unknown arch")
		pushAgentErrs[nodeIP] = err
	}

	if !e.checkAvailableHosts(multiCli, pushAgentErrs) {
		return pushAgentErrs, errors.New("No available hosts to push")
	}

	if err := e.executePreCommand(multiCli, pushAgentErrs); err != nil {
		return pushAgentErrs, err
	}

	if err := e.executeStartCommand(multiCli, localKubeConfig, servicePath, pushAgentErrs); err != nil {
		return pushAgentErrs, err
	}

	postCommand := bkessh.Command{
		Cmds: bkessh.Commands{
			"chmod 755 /usr/local/bin/",
			"chmod 755 /etc/systemd/system/",
		},
	}

	stdErrs, _ := multiCli.Run(postCommand)
	for nodeIP, serrs := range stdErrs.Out() {
		e.Ctx.Log.Warn("PostCommandFailed", "node %s, err: %v", nodeIP, serrs.String())
		pushAgentErrs[nodeIP] = errors.Errorf("Failed to push BKEAgent to node %s, PostCommandFailed err: %s", nodeIP, serrs.String())
	}

	return pushAgentErrs, nil
}

// checkAvailableHosts check if avauliable hosts exist
func (e *EnsureBKEAgent) checkAvailableHosts(multiCli *bkessh.MultiCli, pushAgentErrs map[string]error) bool {
	return len(multiCli.AvailableHosts()) > 0
}

// executePreCommand executes prerequisite commands: Modify folder permissions, stop old services, and clean up related files.
func (e *EnsureBKEAgent) executePreCommand(multiCli *bkessh.MultiCli, pushAgentErrs map[string]error) error {
	preCommand := bkessh.Command{
		Cmds: bkessh.Commands{
			"chmod 777 /usr/local/bin/",
			"chmod 777 /etc/systemd/system/",
			// 忽略输出
			"systemctl stop bkeagent 2>&1 >/dev/null || true",
			"systemctl disable bkeagent 2>&1 >/dev/null || true",
			"systemctl daemon-reload 2>&1 >/dev/null || true",
			"rm -rf /usr/local/bin/bkeagent* 2>&1 >/dev/null || true",
			"rm -f /etc/systemd/system/bkeagent.service 2>&1 >/dev/null || true",
			"rm -rf /etc/openFuyao/bkeagent 2>&1 >/dev/null || true",
		},
	}

	stdErrs, _ := multiCli.Run(preCommand)
	for nodeIP, serrs := range stdErrs.Out() {
		e.Ctx.Log.Warn("PreCommandFailed", "node %s, err: %v", nodeIP, serrs.String())
		if pushAgentErrs != nil {
			pushAgentErrs[nodeIP] = errors.Errorf("Failed to push BKEAgent to node %s, PreCommandFailed err: %s", nodeIP, serrs.String())
		}
		multiCli.RemoveHost(nodeIP)
	}

	if !e.checkAvailableHosts(multiCli, pushAgentErrs) {
		return errors.New("No available hosts to push")
	}
	return nil
}

// executeStartCommand executes the startup command: upload files, configure bkeagent, and start the service.
func (e *EnsureBKEAgent) executeStartCommand(multiCli *bkessh.MultiCli, localKubeConfig []byte, servicePath string, pushAgentErrs map[string]error) error {
	// 准备文件上传列表
	fileUpList := e.prepareFileUploadList(servicePath)

	// push and start bkeagent
	startCommand := bkessh.Command{
		FileUp: fileUpList, // 动态列表：仅包含存在的文件
		Cmds: bkessh.Commands{
			//在要推送的 节点上创建文件夹，且权限正确
			fmt.Sprintf("mkdir -p -m 755 %s ", deployCertDir),
			"mv -f /usr/local/bin/bkeagent_* /usr/local/bin/bkeagent",
			"mkdir -p -m 777 /etc/openFuyao/bkeagent",
			"chmod +x /usr/local/bin/bkeagent",
			// nodeIP and localKubeConfig needs pre-exist before start bkeagent

			fmt.Sprintf("echo -e %q > /etc/openFuyao/bkeagent/config", localKubeConfig),
			"systemctl daemon-reload 2>&1 >/dev/null",
			"systemctl enable bkeagent 2>&1 >/dev/null",
			"systemctl restart bkeagent 2>&1 >/dev/null",
		},
	}

	multiCli.RegisterHostsCustomCmdFunc(phaseutil.HostCustomCmdFunc)
	defer multiCli.RemoveHostsCustomCmdFunc()

	stdErrs, _ := multiCli.Run(startCommand)
	// ignore systemctl enable stderr
	for nodeIP, serrs := range stdErrs.Out() {
		e.Ctx.Log.Warn("StartBKEAgentFailed", "node %s, err: %v", nodeIP, serrs.String())

		var tmpErrs []string
		for _, err := range serrs {
			if strings.Contains(err.Out, "Created symlink") || strings.Contains(err.Out, "Failed to execute operation: File exists") {
				continue
			}
			tmpErrs = append(tmpErrs, err.Out)
		}
		if len(tmpErrs) == 0 {
			delete(pushAgentErrs, nodeIP)
			continue
		}
		if pushAgentErrs != nil {
			pushAgentErrs[nodeIP] = errors.New(strings.Join(tmpErrs, ";"))
		}
		multiCli.RemoveHost(nodeIP)
	}

	if !e.checkAvailableHosts(multiCli, pushAgentErrs) {
		return errors.New("No available hosts to push")
	}

	return nil
}

func (e *EnsureBKEAgent) pingAgent() error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()

	err, successNodesInfo, failedNodesInfo := phaseutil.PingBKEAgent(ctx, c, scheme, bkeCluster)
	if err != nil {
		log.Error(constant.BKEAgentNotReadyReason, "Failed to ping bkeagent: %v", err)
		return err
	}

	e.updateNodeStatus(bkeCluster, successNodesInfo, failedNodesInfo)

	if err = e.validateAndHandleNodesField(); err != nil {
		return err
	}

	// 节点信息现在存储在 BKENode CRD 中，不再需要同步到 BKECluster.Spec.ClusterConfig.Nodes
	if err = mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
		return errors.Wrap(err, "failed to sync status")
	}

	if bkeCluster.Status.ClusterHealthState == bkev1beta1.Deploying && len(failedNodesInfo) > 0 {
		log.Info(constant.BKEAgentUpdatingReason, "at ping bkeagent state, failed nodes: %v", failedNodesInfo)
		// Use NodeFetcher to get BKENodes for checking failed nodes
		bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(ctx, bkeCluster)
		if err != nil {
			log.Warn(constant.BKEAgentUpdatingReason, "Failed to get BKENodes for checking failed nodes: %v", err)
		} else if needCountFailed := phaseutil.GetNotSkipFailedNodeWithBKENodes(bkeNodes, failedNodesInfo); needCountFailed > 0 {
			log.Info(constant.BKEAgentUpdatingReason, "At ping state, not-skip nodes failed: %v", failedNodesInfo)
			return fmt.Errorf("not skip nodes agent need ping success, retry later. failed count: %d", needCountFailed)
		}
	}

	if err = e.checkAllOrPushedAgentsFailed(successNodesInfo, failedNodesInfo); err != nil {
		return err
	}

	return nil
}

func (e *EnsureBKEAgent) updateNodeStatus(
	bkeCluster *bkev1beta1.BKECluster,
	successNodesInfo, failedNodesInfo []string,
) {
	ctx := e.Ctx.Context
	nf := e.Ctx.NodeFetcher()

	for _, node := range failedNodesInfo {
		nodeIP := phaseutil.GetNodeIPFromCommandWaitResult(node)
		if err := e.Ctx.SetNodeStateWithMessage(nodeIP, bkev1beta1.NodeInitFailed, "Failed ping bkeagent"); err != nil {
			e.Ctx.Log.Warn("Failed to set node state for %s: %v", nodeIP, err)
		}
		if err := nf.UnmarkNodeStateFlagForCluster(ctx, bkeCluster, nodeIP, bkev1beta1.NodeAgentPushedFlag); err != nil {
			e.Ctx.Log.Warn("Failed to unmark node state flag for %s: %v", nodeIP, err)
		}
		if err := nf.SetNodeNeedSkip(ctx, bkeCluster.Namespace, bkeCluster.Name, nodeIP, true); err != nil {
			e.Ctx.Log.Warn("Failed to set skip node error for %s: %v", nodeIP, err)
		}
	}

	for _, node := range successNodesInfo {
		nodeIP := phaseutil.GetNodeIPFromCommandWaitResult(node)
		if err := e.Ctx.SetNodeStateMessage(nodeIP, "BKEAgent is ready"); err != nil {
			e.Ctx.Log.Warn(constant.InternalErrorReason, "Failed to set node state message for %s: %v", nodeIP, err)
		}
		if err := nf.MarkNodeStateFlagForCluster(ctx, bkeCluster, nodeIP, bkev1beta1.NodeAgentPushedFlag); err != nil {
			e.Ctx.Log.Warn(constant.InternalErrorReason, "Failed to mark node state flag for %s: %v", nodeIP, err)
		}
		if err := nf.MarkNodeStateFlagForCluster(ctx, bkeCluster, nodeIP, bkev1beta1.NodeAgentReadyFlag); err != nil {
			e.Ctx.Log.Warn(constant.InternalErrorReason, "Failed to mark node state flag for %s: %v", nodeIP, err)
		}
	}
}

func (e *EnsureBKEAgent) validateAndHandleNodesField() error {
	bkeCluster := e.Ctx.BKECluster
	nodes, err := e.Ctx.GetNodes()
	if err != nil {
		return err
	}

	var validationErr error
	if clusterutil.IsBKECluster(bkeCluster) {
		validationErr = bkevalidate.ValidateNodesFields(nodes)
	} else if clusterutil.IsBocloudCluster(bkeCluster) {
		validationErr = bkevalidate.ValidateNonStandardNodesFields(nodes)
	}

	if validationErr == nil {
		return nil
	}

	return e.handleValidationFailure(validationErr)
}

func (e *EnsureBKEAgent) handleValidationFailure(err error) error {
	ctx, c, bkeCluster, _, log := e.Ctx.Untie()
	nf := e.Ctx.NodeFetcher()
	errInfo := fmt.Sprintf("Failed to validate nodes fields: %v", err)

	condition.ConditionMark(bkeCluster, bkev1beta1.BKEConfigCondition, confv1beta1.ConditionFalse, constant.BKEConfigInvalidReason, errInfo)
	log.Error(constant.BKEAgentNotReadyReason, err.Error())

	if strings.Contains(err.Error(), "hostname is not unique") {
		detailedMsg := fmt.Sprintf(
			"Some nodes have duplicate hostnames. Please fix or set explicitly. err: %v", err,
		)
		condition.ConditionMark(bkeCluster, bkev1beta1.BKEConfigCondition, confv1beta1.ConditionFalse, constant.HostNameNotUniqueReason, detailedMsg)
		log.Error(constant.HostNameNotUniqueReason, detailedMsg)

		for _, node := range e.needPushNodes {
			log.Error(constant.HostNameNotUniqueReason, "IP: %s Hostname: %s", node.IP, node.Hostname)
			if err := nf.UnmarkNodeStateFlagForCluster(ctx, bkeCluster, node.IP, bkev1beta1.NodeAgentPushedFlag); err != nil {
				log.Warn("Failed to unmark node state flag for %s: %v", node.IP, err)
			}
			if err := nf.UnmarkNodeStateFlagForCluster(ctx, bkeCluster, node.IP, bkev1beta1.NodeAgentReadyFlag); err != nil {
				log.Warn("Failed to unmark node state flag for %s: %v", node.IP, err)
			}
			if err := e.Ctx.SetNodeStateWithMessage(node.IP, bkev1beta1.NodeInitFailed, detailedMsg); err != nil {
				log.Warn("Failed to set node state for %s: %v", node.IP, err)
			}
		}
	}

	if errSync := mergecluster.SyncStatusUntilComplete(c, bkeCluster); errSync != nil {
		return errors.Wrap(errSync, "failed to sync status after validation failure")
	}

	return err
}

func (e *EnsureBKEAgent) checkAllOrPushedAgentsFailed(successNodesInfo, failedNodesInfo []string) error {
	_, _, _, _, log := e.Ctx.Untie()

	if len(successNodesInfo) == 0 {
		log.Error(constant.BKEAgentNotReadyReason, "Failed to ping all nodes' bkeagent")
		return errors.New("failed to ping all nodes' bkeagent")
	}

	if len(failedNodesInfo) > 0 && e.allNeedPushNodesFailed(failedNodesInfo) {
		return fmt.Errorf("none of the nodes that need to push the agent can be pinged, failed: %v", failedNodesInfo)
	}

	return nil
}

func (e *EnsureBKEAgent) allNeedPushNodesFailed(failedNodesInfo []string) bool {
	if len(e.needPushNodes) == 0 {
		return false
	}

	failedIPs := make(map[string]bool, len(failedNodesInfo))
	for _, failInfo := range failedNodesInfo {
		ip := phaseutil.GetNodeIPFromCommandWaitResult(failInfo)
		failedIPs[ip] = true
	}

	for _, node := range e.needPushNodes {
		if !failedIPs[node.IP] {
			return false
		}
	}

	return true
}

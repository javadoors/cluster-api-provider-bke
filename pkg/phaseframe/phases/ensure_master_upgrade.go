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

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	containerutil "sigs.k8s.io/cluster-api/util/container"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/mfutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	l "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
)

const (
	EnsureMasterUpgradeName confv1beta1.BKEClusterPhase = "EnsureMasterUpgrade"
	// MasterUpgradePollIntervalSeconds 等待master升级时轮询间隔秒数
	MasterUpgradePollIntervalSeconds = 2
	// MasterUpgradeTimeoutMinutes 等待master升级超时分钟数
	MasterUpgradeTimeoutMinutes = 5
)

type EnsureMasterUpgrade struct {
	phaseframe.BasePhase
}

func NewEnsureMasterUpgrade(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	ctx.Log.NormalLogger = l.Named(EnsureMasterUpgradeName.String())
	base := phaseframe.NewBasePhase(ctx, EnsureMasterUpgradeName)
	return &EnsureMasterUpgrade{BasePhase: base}
}

func (e *EnsureMasterUpgrade) Execute() (ctrl.Result, error) {
	if v, ok := annotation.HasAnnotation(e.Ctx.BKECluster, "deployAction"); !ok || v != "k8s_upgrade" {
		//添加boc所需的注解
		patchFunc := func(bkeCluster *bkev1beta1.BKECluster) {
			annotation.SetAnnotation(bkeCluster, "deployAction", "k8s_upgrade")
		}
		if err := mergecluster.SyncStatusUntilComplete(e.Ctx.Client, e.Ctx.BKECluster, patchFunc); err != nil {
			return ctrl.Result{}, err
		}
	}

	return e.reconcileMasterUpgrade()
}

func (e *EnsureMasterUpgrade) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}

	bkeNodes, ok := fetchBKENodesIfCPInitialized(e.Ctx, new)
	if !ok {
		return false
	}
	nodes := phaseutil.GetNeedUpgradeMasterNodesWithBKENodes(new, bkeNodes)
	if nodes.Length() == 0 {
		return false
	}

	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

func (e *EnsureMasterUpgrade) reconcileMasterUpgrade() (ctrl.Result, error) {
	_, _, bkeCluster, _, log := e.Ctx.Untie()
	if bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion != bkeCluster.Status.KubernetesVersion {
		ret, err := e.rolloutUpgrade()
		if err != nil {
			return ret, err
		}
	}
	log.Info(constant.MasterUpgradedReason, "k8s version same, not need to upgrade master node")
	return ctrl.Result{}, nil
}

func (e *EnsureMasterUpgrade) rolloutUpgrade() (ctrl.Result, error) {
	ctx, c, bkeCluster, _, log := e.Ctx.Untie()

	needUpgradeNodes, err := e.getNeedUpgradeNodes(bkeCluster, log)
	if err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	// 检查etcd配置
	specNodes, _ := e.Ctx.NodeFetcher().GetNodesForBKECluster(e.Ctx, bkeCluster)
	needBackupEtcd := false
	backEtcdNode := confv1beta1.Node{}
	etcdNodes := specNodes.Etcd()
	if etcdNodes.Length() != 0 {
		needBackupEtcd = true
		backEtcdNode = etcdNodes[0]
		log.Info(constant.MasterUpgradingReason, "backup etcd data to node %s", phaseutil.NodeInfo(backEtcdNode))
	}

	if err := e.ensureEtcdAdvertiseClientUrlsAnnotation(etcdNodes); err != nil {
		log.Error(constant.MasterUpgradeFailedReason, "ensure etcd advertise client urls annotation failed, err: %v", err)
		return ctrl.Result{}, errors.Errorf("ensure etcd advertise client urls annotation failed, err: %v", err)
	}

	// 升级节点
	upgradeParams := UpgradeMasterNodesParams{
		Ctx:              ctx,
		Client:           c,
		BKECluster:       bkeCluster,
		NeedUpgradeNodes: needUpgradeNodes,
		NeedBackupEtcd:   needBackupEtcd,
		BackEtcdNode:     backEtcdNode,
		Log:              log,
	}
	if err := e.upgradeMasterNodesWithParams(upgradeParams); err != nil {
		return ctrl.Result{}, err
	}

	log.Info(constant.MasterUpgradeSucceedReason, "upgrade all master success")
	// master 始终是最后更新完的,这时候更改status的版本
	bkeCluster.Status.KubernetesVersion = bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion

	// 更新addon版本
	return e.updateAddonVersions(c, bkeCluster, log)
}

// getNeedUpgradeNodes 获取需要升级的节点
func (e *EnsureMasterUpgrade) getNeedUpgradeNodes(bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger) (bkenode.Nodes, error) {
	needUpgradeNodes := bkenode.Nodes{}

	// Use NodeFetcher to get BKENodes from API server
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
	if err != nil {
		log.Warn(constant.MasterUpgradingReason, "failed to get BKENodes: %v", err)
		return nil, errors.Wrap(err, "failed to get BKENodes")
	}
	nodes := phaseutil.GetNeedUpgradeMasterNodesWithBKENodes(bkeCluster, bkeNodes)
	for _, node := range nodes {
		nodeState, _ := e.Ctx.NodeFetcher().GetNodeStateFlagForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeAgentReadyFlag)
		if !nodeState {
			log.Info(constant.MasterUpgradingReason, "agent is not ready at node %s, skip upgrade", phaseutil.NodeInfo(node))
			continue
		}
		needUpgradeNodes = append(needUpgradeNodes, node)
	}

	if len(needUpgradeNodes) == 0 {
		log.Info(constant.MasterUpgradingReason, "all the master node BKEAgent is not ready")
		return nil, errors.New("all the master node BKEAgent is not ready")
	}

	return needUpgradeNodes, nil
}

// UpgradeMasterNodesParams 包含升级master节点所需的参数
type UpgradeMasterNodesParams struct {
	Ctx              context.Context
	Client           client.Client
	BKECluster       *bkev1beta1.BKECluster
	NeedUpgradeNodes bkenode.Nodes
	NeedBackupEtcd   bool
	BackEtcdNode     confv1beta1.Node
	Log              *bkev1beta1.BKELogger
}

// upgradeMasterNodes 升级master节点
func (e *EnsureMasterUpgrade) upgradeMasterNodes(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, needUpgradeNodes bkenode.Nodes, needBackupEtcd bool, backEtcdNode confv1beta1.Node, log *bkev1beta1.BKELogger) error {
	params := UpgradeMasterNodesParams{
		Ctx:              ctx,
		Client:           c,
		BKECluster:       bkeCluster,
		NeedUpgradeNodes: needUpgradeNodes,
		NeedBackupEtcd:   needBackupEtcd,
		BackEtcdNode:     backEtcdNode,
		Log:              log,
	}
	return e.upgradeMasterNodesWithParams(params)
}

// upgradeMasterNodesWithParams 使用参数结构体升级master节点
func (e *EnsureMasterUpgrade) upgradeMasterNodesWithParams(params UpgradeMasterNodesParams) error {
	params.Log.Info(constant.MasterUpgradingReason, "Start upgrade master nodes process, upgrade policy: rollingUpgrade")

	clientSet, _, _ := kube.GetTargetClusterClient(params.Ctx, params.Client, params.BKECluster)
	nodeFetcher := e.Ctx.NodeFetcher()
	for _, node := range params.NeedUpgradeNodes {
		remoteNode, err := phaseutil.GetRemoteNodeByBKENode(params.Ctx, clientSet, node)
		if err != nil {
			params.Log.Error(constant.WorkerUpgradeFailedReason, "get remote cluster Node resource failed, err: %v", err)
			return errors.Errorf("get remote cluster Node resource failed, err: %v", err)
		}
		// 已经是期望版本的节点不需要升级
		if remoteNode.Status.NodeInfo.KubeletVersion == params.BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion {
			params.Log.Info(constant.MasterUpgradeSucceedReason, "node %q is already the expected version %q,skip upgrade", phaseutil.NodeInfo(node), params.BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion)
			continue
		}

		// mark node as upgrading
		nodeFetcher.SetNodeStateWithMessageForCluster(params.Ctx, params.BKECluster, node.IP, bkev1beta1.NodeUpgrading, "Upgrading")
		if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
			return err
		}

		if err := e.upgradeNode(params.NeedBackupEtcd, params.BackEtcdNode, node, remoteNode); err != nil {
			// master node block until upgrade success
			params.Log.Error(constant.MasterUpgradeFailedReason, "upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
			nodeFetcher.SetNodeStateWithMessageForCluster(params.Ctx, params.BKECluster, node.IP, bkev1beta1.NodeUpgradeFailed, err.Error())
			if err = mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
				return errors.Errorf("upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
			}
			return errors.Errorf("upgrade node %q failed: %v", phaseutil.NodeInfo(node), err)
		}
		// mark node as upgrading success
		nodeFetcher.SetNodeStateWithMessageForCluster(params.Ctx, params.BKECluster, node.IP, bkev1beta1.NodeNotReady, "Upgrading success")
		if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
			return err
		}
	}
	return nil
}

// updateAddonVersions 更新addon版本
func (e *EnsureMasterUpgrade) updateAddonVersions(c client.Client, bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger) (ctrl.Result, error) {
	// 更新addon version kubeproxy
	kubeproxyNeedUpgrade := false
	kubectlNeedUpgrade := false
	for _, addon := range bkeCluster.Spec.ClusterConfig.Addons {
		if addon.Name == "kubeproxy" && addon.Version != bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion {
			log.Info(constant.MasterUpgradingReason, "kubeproxy need upgrade")
			kubeproxyNeedUpgrade = true
		}
		if addon.Name == "kubectl" && addon.Version != "v1.25" {
			log.Info(constant.MasterUpgradingReason, "kubectl need upgrade")
			kubectlNeedUpgrade = true
		}
	}

	var patchFuncs []mergecluster.PatchFunc

	if kubeproxyNeedUpgrade {
		if err := e.upgradeKubeProxy(bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion); err != nil {
			log.Error(constant.MasterUpgradeFailedReason, "upgrade kubeproxy failed: %v", err)
			return ctrl.Result{}, err
		}
		patchFunc := func(currentCombinedBkeCluster *bkev1beta1.BKECluster) {
			for i, d := range currentCombinedBkeCluster.Spec.ClusterConfig.Addons {
				if d.Name == "kubeproxy" {
					d.Version = currentCombinedBkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion
					currentCombinedBkeCluster.Spec.ClusterConfig.Addons[i] = d
				}
			}
			for i, d := range currentCombinedBkeCluster.Status.AddonStatus {
				addon := d.DeepCopy()
				if addon.Name == "kubeproxy" {
					addon.Version = currentCombinedBkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion
					currentCombinedBkeCluster.Status.AddonStatus[i] = *addon
				}
			}
		}
		patchFuncs = append(patchFuncs, patchFunc)
	}

	if kubectlNeedUpgrade {
		patchFunc := func(currentCombinedBkeCluster *bkev1beta1.BKECluster) {
			found := false
			for i, d := range currentCombinedBkeCluster.Spec.ClusterConfig.Addons {
				if d.Name == "kubectl" {
					found = true
					d.Version = "v1.25"
					currentCombinedBkeCluster.Spec.ClusterConfig.Addons[i] = d
				}
			}
			if !found {
				currentCombinedBkeCluster.Spec.ClusterConfig.Addons = append(currentCombinedBkeCluster.Spec.ClusterConfig.Addons, confv1beta1.Product{
					Name:    "kubectl",
					Version: "v1.25",
					Block:   false,
				})
			}
		}
		patchFuncs = append(patchFuncs, patchFunc)
	}

	if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster, patchFuncs...); err != nil {
		return ctrl.Result{}, errors.Errorf("failed to upgrade addon version, err: %v", err)
	}
	return ctrl.Result{}, nil
}

func (e *EnsureMasterUpgrade) upgradeNode(needBackupEtcd bool, backEtcdNode confv1beta1.Node, node confv1beta1.Node, remoteNode *corev1.Node) error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()

	// 执行节点升级
	upgradeParams := ExecuteNodeUpgradeParams{
		Ctx:            ctx,
		Client:         c,
		BKECluster:     bkeCluster,
		Scheme:         scheme,
		Log:            log,
		NeedBackupEtcd: needBackupEtcd,
		BackEtcdNode:   backEtcdNode,
		Node:           node,
	}
	if err := e.executeNodeUpgradeWithParams(upgradeParams); err != nil {
		return err
	}

	log.Info(constant.MasterUpgradingReason, "upgrade node %q operation succeed", phaseutil.NodeInfo(node))

	// 等待节点健康检查通过
	healthCheckParams := WaitForNodeHealthCheckParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Log:        log,
		Node:       node,
	}
	return e.waitForNodeHealthCheckWithParams(healthCheckParams)
}

// ExecuteNodeUpgradeParams 包含执行节点升级所需的参数
type ExecuteNodeUpgradeParams struct {
	Ctx            context.Context
	Client         client.Client
	BKECluster     *bkev1beta1.BKECluster
	Scheme         *runtime.Scheme
	Log            *bkev1beta1.BKELogger
	NeedBackupEtcd bool
	BackEtcdNode   confv1beta1.Node
	Node           confv1beta1.Node
}

// executeNodeUpgrade 执行节点升级
func (e *EnsureMasterUpgrade) executeNodeUpgrade(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, scheme *runtime.Scheme, log *bkev1beta1.BKELogger, needBackupEtcd bool, backEtcdNode confv1beta1.Node, node confv1beta1.Node) error {
	params := ExecuteNodeUpgradeParams{
		Ctx:            ctx,
		Client:         c,
		BKECluster:     bkeCluster,
		Scheme:         scheme,
		Log:            log,
		NeedBackupEtcd: needBackupEtcd,
		BackEtcdNode:   backEtcdNode,
		Node:           node,
	}
	return e.executeNodeUpgradeWithParams(params)
}

// executeNodeUpgradeWithParams 使用参数结构体执行节点升级
func (e *EnsureMasterUpgrade) executeNodeUpgradeWithParams(params ExecuteNodeUpgradeParams) error {
	masterParams := CreateUpgradeCommandParams{
		Ctx:         params.Ctx,
		Namespace:   params.BKECluster.Namespace,
		Client:      params.Client,
		Scheme:      params.Scheme,
		OwnerObj:    params.BKECluster,
		ClusterName: params.BKECluster.Name,
		Node:        &params.Node,
		BKEConfig:   params.BKECluster.Name,
		Phase:       bkev1beta1.UpgradeControlPlane,
	}
	upgrade := createUpgradeCommand(masterParams)

	if params.NeedBackupEtcd && params.Node.IP == params.BackEtcdNode.IP {
		upgrade.BackUpEtcd = true
	}

	params.Log.Info(constant.MasterUpgradingReason, "start upgrade node %s", phaseutil.NodeInfo(params.Node))
	if err := upgrade.New(); err != nil {
		params.Log.Error(constant.MasterUpgradeFailedReason, "upgrade node %q failed: %v", phaseutil.NodeInfo(params.Node), err)
		return errors.Errorf("create upgrade command，node: %q failed: %v", phaseutil.NodeInfo(params.Node), err)
	}
	params.Log.Info(constant.MasterUpgradingReason, "wait upgrade node %s finish", phaseutil.NodeInfo(params.Node))
	err, _, failedNodes := upgrade.Wait()
	if err != nil {
		params.Log.Error(constant.MasterUpgradeFailedReason, "wait upgrade command complete failed，node: %q, err: %v", phaseutil.NodeInfo(params.Node), err)
		return errors.Errorf("wait upgrade command complete failed，node: %q, err: %v", phaseutil.NodeInfo(params.Node), err)
	}
	if len(failedNodes) != 0 {
		params.Log.Error(constant.MasterUpgradeFailedReason, "upgrade node %q failed: %v", phaseutil.NodeInfo(params.Node), err)
		commandErrs, err := phaseutil.LogCommandFailed(*upgrade.Command, failedNodes, params.Log, constant.MasterUpgradeFailedReason)
		phaseutil.MarkNodeStatusByCommandErrs(params.Ctx, params.Client, params.BKECluster, commandErrs)
		return errors.Errorf("upgrade node %q failed: %v", phaseutil.NodeInfo(params.Node), err)
	}
	return nil
}

// WaitForNodeHealthCheckParams 包含等待节点健康检查所需的参数
type WaitForNodeHealthCheckParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Log        *bkev1beta1.BKELogger
	Node       confv1beta1.Node
}

// waitForNodeHealthCheck 等待节点健康检查通过
func (e *EnsureMasterUpgrade) waitForNodeHealthCheck(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger, node confv1beta1.Node) error {
	params := WaitForNodeHealthCheckParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Log:        log,
		Node:       node,
	}
	return e.waitForNodeHealthCheckWithParams(params)
}

// waitForNodeHealthCheckWithParams 使用参数结构体等待节点健康检查通过
func (e *EnsureMasterUpgrade) waitForNodeHealthCheckWithParams(params WaitForNodeHealthCheckParams) error {
	remoteClient, err := kube.NewRemoteClientByBKECluster(params.Ctx, params.Client, params.BKECluster)
	if err != nil {
		params.Log.Error(constant.MasterUpgradeFailedReason, "get remote client for BKECluster %q failed", utils.ClientObjNS(params.BKECluster))
		return errors.Errorf("get remote client for BKECluster %q failed: %v", utils.ClientObjNS(params.BKECluster), err)
	}
	clientSet, _ := remoteClient.KubeClient()

	// wait for node pass healthy check
	params.Log.Info(constant.MasterUpgradingReason, "wait for node %q pass healthy check", phaseutil.NodeInfo(params.Node))
	masterParams := WaitForWorkerNodeHealthCheckParams{
		Ctx:          params.Ctx,
		ClientSet:    clientSet,
		RemoteClient: remoteClient,
		Node:         params.Node,
		K8sVersion:   params.BKECluster.Spec.ClusterConfig.Cluster.KubernetesVersion,
		Logger:       params.Log,
	}
	err = waitForWorkerNodeHealthCheck(masterParams)

	if err != nil {
		params.Log.Error(constant.MasterUpgradeFailedReason, "upgrade node %q failed: %v", phaseutil.NodeInfo(params.Node), err)
		return errors.Errorf("wait for node %q pass healthy check failed: %v", phaseutil.NodeInfo(params.Node), err)
	}
	params.Log.Info(constant.MasterUpgradingReason, "upgrade master node %q success", phaseutil.NodeInfo(params.Node))
	return nil
}

func (e *EnsureMasterUpgrade) upgradeKubeProxy(expectVersion string) error {
	ctx, c, bkeCluster, _, log := e.Ctx.Untie()
	clientSet, _, err := kube.GetTargetClusterClient(ctx, c, bkeCluster)
	if err != nil {
		return err
	}

	// get ds
	ds, err := clientSet.AppsV1().DaemonSets(metav1.NamespaceSystem).Get(ctx, "kube-proxy", metav1.GetOptions{})
	if err != nil {
		log.Error(constant.MasterUpgradeFailedReason, "get kube-proxy ds failed: %v", err)
		return err
	}

	cfg := bkeinit.BkeConfig(*bkeCluster.Spec.ClusterConfig)
	imageRepo := cfg.ImageFuyaoRepo()

	// update image
	srcImage := ds.Spec.Template.Spec.Containers[0].Image
	srcImage, err = containerutil.ModifyImageRepository(srcImage, imageRepo)
	if err != nil {
		return err
	}
	dstImage, err := containerutil.ModifyImageTag(srcImage, expectVersion)
	if err != nil {
		return err
	}
	ds.Spec.Template.Spec.Containers[0].Image = dstImage

	_, err = clientSet.AppsV1().DaemonSets(metav1.NamespaceSystem).Update(ctx, ds, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	log.Info(constant.MasterUpgradingReason, "update kube-proxy image to %q success", dstImage)
	return nil
}

// ensureEtcdAdvertiseClientUrlsAnnotation
// ensure EtcdAdvertiseClientUrlsAnnotation exit in etcd pod annotations
func (e *EnsureMasterUpgrade) ensureEtcdAdvertiseClientUrlsAnnotation(etcdNodes bkenode.Nodes) error {
	ctx, c, bkeCluster, _, _ := e.Ctx.Untie()
	clientSet, _, err := kube.GetTargetClusterClient(ctx, c, bkeCluster)
	if err != nil {
		return err
	}
	for _, n := range etcdNodes {
		etcdPodName := kube.StaticPodName(mfutil.Etcd, n.Hostname)
		podClient := clientSet.CoreV1().Pods(metav1.NamespaceSystem)
		pod, err := podClient.Get(ctx, etcdPodName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		annotations := pod.GetAnnotations()
		if v, ok := annotations[annotation.EtcdAdvertiseClientUrlsAnnotationKey]; ok && v != "" {
			continue
		}
		annotations[annotation.EtcdAdvertiseClientUrlsAnnotationKey] = phaseutil.GetClientURLByIP(n.IP)
		pod.SetAnnotations(annotations)
		if _, err = podClient.Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	return nil
}

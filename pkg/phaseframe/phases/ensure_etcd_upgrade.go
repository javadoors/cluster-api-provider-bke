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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
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
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/mfutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	l "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
)

const (
	// EnsureEtcdUpgradeName represents the phase name for etcd upgrade operations
	EnsureEtcdUpgradeName confv1beta1.BKEClusterPhase = "EnsureEtcdUpgrade"
	// PollImmeInternal is used to define internal time for wait.PollImmediate, 500ms
	PollImmeInternal = 500 * time.Millisecond
	// PollImmeTimeout is used to define timeout time for wait.PollImmediate, min
	PollImmeTimeout = 3 * time.Minute
)

const (
	// EtcdHealthCheckInterval defines the interval between etcd health check attempts
	EtcdHealthCheckInterval = 2 * time.Second
	// EtcdHealthCheckTimeout defines the maximum time to wait for etcd to become healthy
	EtcdHealthCheckTimeout = 5 * time.Minute
)

// NodeUpgradeParams encapsulates parameters for upgrading etcd nodes
type NodeUpgradeParams struct {
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Nodes      bkenode.Nodes
	NeedBackup bool
	BackupNode confv1beta1.Node
	Log        *bkev1beta1.BKELogger
}

// SingleNodeUpgradeParams encapsulates parameters for upgrading a single etcd node
type SingleNodeUpgradeParams struct {
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Node       confv1beta1.Node
	NeedBackup bool
	BackupNode confv1beta1.Node
	Log        *bkev1beta1.BKELogger
}

// EtcdUpgradeParams encapsulates parameters for etcd upgrade operation
type EtcdUpgradeParams struct {
	NeedBackup bool
	BackupNode confv1beta1.Node
	Node       confv1beta1.Node
	Version    string
}

// UpgradeCommandParams encapsulates parameters for creating upgrade command
type UpgradeCommandParams struct {
	Ctx        context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Scheme     *runtime.Scheme
	Node       confv1beta1.Node
	NeedBackup bool
	BackupNode confv1beta1.Node
}

// NodeStatusParams encapsulates parameters for node status operations
type NodeStatusParams struct {
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Node       confv1beta1.Node
}

// UpgradeFailureParams encapsulates parameters for handling upgrade failure
type UpgradeFailureParams struct {
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Node       confv1beta1.Node
	Error      error
	Log        *bkev1beta1.BKELogger
}

// WaitUpgradeParams encapsulates parameters for waiting upgrade completion
type WaitUpgradeParams struct {
	Upgrade    *command.Upgrade
	BKECluster *bkev1beta1.BKECluster
	Node       confv1beta1.Node
	Log        *bkev1beta1.BKELogger
}

// HealthCheckParams encapsulates parameters for etcd health check
type HealthCheckParams struct {
	Ctx     context.Context
	Node    confv1beta1.Node
	Version string
	Log     *bkev1beta1.BKELogger
}

// EnsureEtcdUpgrade is a phase implementation that handles etcd upgrade operations.
// It manages the rolling upgrade of etcd components across cluster nodes,
// including backup, version verification, and health checks.
type EnsureEtcdUpgrade struct {
	phaseframe.BasePhase
}

// NewEnsureEtcdUpgrade creates a new EnsureEtcdUpgrade phase instance.
// It initializes the phase with the provided context and sets up the logger
// with the phase name.
func NewEnsureEtcdUpgrade(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	ctx.Log.NormalLogger = l.Named(EnsureEtcdUpgradeName.String())
	base := phaseframe.NewBasePhase(ctx, EnsureEtcdUpgradeName)
	return &EnsureEtcdUpgrade{BasePhase: base}
}

// Execute performs the etcd upgrade phase execution.
// It orchestrates the reconciliation process for upgrading etcd components
// across the cluster nodes according to the specified version in the cluster configuration.
// Returns a ctrl.Result for requeueing if needed and any error encountered during execution.
func (e *EnsureEtcdUpgrade) Execute() (ctrl.Result, error) {
	return e.reconcileEtcdUpgrade()
}

// NeedExecute determines whether the etcd upgrade phase needs to be executed.
// It compares the old and new BKECluster configurations to check if the etcd version
// has changed and requires an upgrade operation.
// Returns true if upgrade is needed, false otherwise.
func (e *EnsureEtcdUpgrade) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}

	if new.Spec.ClusterConfig.Cluster.EtcdVersion == new.Status.EtcdVersion ||
		len(new.Spec.ClusterConfig.Cluster.EtcdVersion) == 0 ||
		len(new.Status.EtcdVersion) == 0 {
		return false
	}

	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

func (e *EnsureEtcdUpgrade) reconcileEtcdUpgrade() (ctrl.Result, error) {
	_, _, bkeCluster, _, log := e.Ctx.Untie()
	if bkeCluster.Spec.ClusterConfig.Cluster.EtcdVersion != bkeCluster.Status.EtcdVersion &&
		len(bkeCluster.Spec.ClusterConfig.Cluster.EtcdVersion) != 0 &&
		len(bkeCluster.Status.EtcdVersion) != 0 {
		ret, err := e.rolloutUpgrade()
		if err != nil {
			return ret, err
		}
	}

	log.Info(constant.EtcdUpgradedReason, "etcd version same, not need to upgrade")
	return ctrl.Result{}, nil
}

func (e *EnsureEtcdUpgrade) rolloutUpgrade() (ctrl.Result, error) {
	_, c, bkeCluster, _, log := e.Ctx.Untie()

	needUpgradeNodes, err := e.filterUpgradeableNodes(bkeCluster, log)
	if err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	needBackupEtcd, backEtcdNode := e.determineBackupNode(bkeCluster, log)

	log.Info(constant.MasterUpgradingReason, "Start upgrade etcd nodes process, upgrade policy: rollingUpgrade")

	params := NodeUpgradeParams{
		Client:     c,
		BKECluster: bkeCluster,
		Nodes:      needUpgradeNodes,
		NeedBackup: needBackupEtcd,
		BackupNode: backEtcdNode,
		Log:        log,
	}
	if err := e.upgradeNodes(params); err != nil {
		return ctrl.Result{}, err
	}

	return e.finalizeUpgrade(c, bkeCluster, log)
}

func (e *EnsureEtcdUpgrade) filterUpgradeableNodes(
	bkeCluster *bkev1beta1.BKECluster,
	log *bkev1beta1.BKELogger,
) (bkenode.Nodes, error) {
	needUpgradeNodes := bkenode.Nodes{}
	// Use NodeFetcher to get BKENodes from API server
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
	if err != nil {
		log.Warn(constant.EtcdUpgradingReason, "failed to get BKENodes: %v", err)
		return nil, errors.Wrap(err, "failed to get BKENodes")
	}
	nodes := phaseutil.GetNeedUpgradeEtcdsWithBKENodes(bkeCluster, bkeNodes)

	for _, node := range nodes {
		nodeState, _ := e.Ctx.NodeFetcher().GetNodeStateFlagForCluster(e.Ctx, bkeCluster, node.IP, bkev1beta1.NodeAgentReadyFlag)
		if !nodeState {
			log.Info(constant.EtcdUpgradingReason, "agent is not ready at node %s, skip upgrade", phaseutil.NodeInfo(node))
			continue
		}
		needUpgradeNodes = append(needUpgradeNodes, node)
	}

	if len(needUpgradeNodes) == 0 {
		log.Info(constant.EtcdUpgradingReason, "all the master node BKEAgent is not ready")
		return nil, errors.New("all the master node BKEAgent is not ready")
	}

	return needUpgradeNodes, nil
}

func (e *EnsureEtcdUpgrade) determineBackupNode(
	bkeCluster *bkev1beta1.BKECluster,
	log *bkev1beta1.BKELogger,
) (bool, confv1beta1.Node) {
	specNodes, _ := e.Ctx.NodeFetcher().GetNodesForBKECluster(e.Ctx, bkeCluster)
	etcdNodes := specNodes.Etcd()

	if etcdNodes.Length() == 0 {
		return false, confv1beta1.Node{}
	}

	backEtcdNode := etcdNodes[0]
	log.Info(constant.EtcdUpgradingReason, "backup etcd data to node %s", phaseutil.NodeInfo(backEtcdNode))
	return true, backEtcdNode
}

func (e *EnsureEtcdUpgrade) upgradeNodes(params NodeUpgradeParams) error {
	for _, node := range params.Nodes {
		singleNodeParams := SingleNodeUpgradeParams{
			Client:     params.Client,
			BKECluster: params.BKECluster,
			Node:       node,
			NeedBackup: params.NeedBackup,
			BackupNode: params.BackupNode,
			Log:        params.Log,
		}
		if err := e.upgradeSingleNode(singleNodeParams); err != nil {
			return err
		}
	}
	params.Log.Info(constant.EtcdUpgradeSuccess, "upgrade all etcd success")
	return nil
}

func (e *EnsureEtcdUpgrade) upgradeSingleNode(params SingleNodeUpgradeParams) error {
	if skip, err := e.shouldSkipNode(params.BKECluster, params.Node, params.Log); err != nil {
		return err
	} else if skip {
		return nil
	}

	nodeStatusParams := NodeStatusParams{
		Client:     params.Client,
		BKECluster: params.BKECluster,
		Node:       params.Node,
	}
	if err := e.markNodeUpgrading(nodeStatusParams); err != nil {
		return err
	}

	upgradeParams := EtcdUpgradeParams{
		NeedBackup: params.NeedBackup,
		BackupNode: params.BackupNode,
		Node:       params.Node,
		Version:    params.BKECluster.Spec.ClusterConfig.Cluster.EtcdVersion,
	}
	if err := e.upgradeEtcd(upgradeParams); err != nil {
		failureParams := UpgradeFailureParams{
			Client:     params.Client,
			BKECluster: params.BKECluster,
			Node:       params.Node,
			Error:      err,
			Log:        params.Log,
		}
		return e.handleUpgradeFailure(failureParams)
	}

	return e.markNodeUpgradeSuccess(nodeStatusParams)
}

func (e *EnsureEtcdUpgrade) shouldSkipNode(
	bkeCluster *bkev1beta1.BKECluster,
	node confv1beta1.Node,
	log *bkev1beta1.BKELogger,
) (bool, error) {
	version, err := e.getEtcdImageVersion(node)
	if err != nil {
		log.Error(constant.EtcdUpgradeFailed, "get remote cluster pod resource failed, err: %v", err)
		return false, errors.Errorf("get remote cluster pod resource failed, err: %v", err)
	}

	if strings.Contains(bkeCluster.Spec.ClusterConfig.Cluster.EtcdVersion, version) {
		log.Info(constant.EtcdUpgradeSuccess, "etcd %q is already the expected version %q,skip upgrade",
			phaseutil.NodeInfo(node), bkeCluster.Spec.ClusterConfig.Cluster.EtcdVersion)
		return true, nil
	}

	return false, nil
}

func (e *EnsureEtcdUpgrade) markNodeUpgrading(params NodeStatusParams) error {
	e.Ctx.NodeFetcher().SetNodeStateWithMessageForCluster(e.Ctx, params.BKECluster, params.Node.IP, bkev1beta1.EtcdUpgrading, "Upgrading")
	return mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster)
}

func (e *EnsureEtcdUpgrade) markNodeUpgradeSuccess(params NodeStatusParams) error {
	e.Ctx.NodeFetcher().SetNodeStateWithMessageForCluster(e.Ctx, params.BKECluster, params.Node.IP, bkev1beta1.EtcdUpgrading, "Upgrading success")
	return mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster)
}

func (e *EnsureEtcdUpgrade) handleUpgradeFailure(params UpgradeFailureParams) error {
	params.Log.Error(constant.EtcdUpgradeFailed, "upgrade node %q failed: %v",
		phaseutil.NodeInfo(params.Node), params.Error)
	e.Ctx.NodeFetcher().SetNodeStateWithMessageForCluster(e.Ctx, params.BKECluster, params.Node.IP, bkev1beta1.EtcdUpgradeFailed, params.Error.Error())
	if err := mergecluster.SyncStatusUntilComplete(params.Client, params.BKECluster); err != nil {
		return errors.Errorf("upgrade node %q failed: %v", phaseutil.NodeInfo(params.Node), err)
	}
	return errors.Errorf("upgrade node %q failed: %v", phaseutil.NodeInfo(params.Node), params.Error)
}

func (e *EnsureEtcdUpgrade) finalizeUpgrade(
	c client.Client,
	bkeCluster *bkev1beta1.BKECluster,
	log *bkev1beta1.BKELogger,
) (ctrl.Result, error) {
	bkeCluster.Status.EtcdVersion = bkeCluster.Spec.ClusterConfig.Cluster.EtcdVersion
	var patchFuncs []mergecluster.PatchFunc

	if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster, patchFuncs...); err != nil {
		return ctrl.Result{}, errors.Errorf("failed to upgrade addon version, err: %v", err)
	}
	return ctrl.Result{}, nil
}

func (e *EnsureEtcdUpgrade) upgradeEtcd(params EtcdUpgradeParams) error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()

	cmdParams := UpgradeCommandParams{
		Ctx:        ctx,
		Client:     c,
		BKECluster: bkeCluster,
		Scheme:     scheme,
		Node:       params.Node,
		NeedBackup: params.NeedBackup,
		BackupNode: params.BackupNode,
	}
	upgrade := e.createUpgradeCommand(cmdParams)

	if err := e.executeUpgradeCommand(&upgrade, params.Node, log); err != nil {
		return err
	}

	waitParams := WaitUpgradeParams{
		Upgrade:    &upgrade,
		BKECluster: bkeCluster,
		Node:       params.Node,
		Log:        log,
	}
	if err := e.waitForUpgradeComplete(waitParams); err != nil {
		return err
	}

	healthParams := HealthCheckParams{
		Ctx:     ctx,
		Node:    params.Node,
		Version: params.Version,
		Log:     log,
	}
	if err := e.waitForEtcdHealthCheck(healthParams); err != nil {
		return err
	}

	log.Info(constant.EtcdUpgradingReason, "upgrade etcd %q success", phaseutil.NodeInfo(params.Node))
	return nil
}

func (e *EnsureEtcdUpgrade) createUpgradeCommand(params UpgradeCommandParams) command.Upgrade {
	upgrade := command.Upgrade{
		BaseCommand: command.BaseCommand{
			Ctx:         params.Ctx,
			NameSpace:   params.BKECluster.Namespace,
			Client:      params.Client,
			Scheme:      params.Scheme,
			OwnerObj:    params.BKECluster,
			ClusterName: params.BKECluster.Name,
			Unique:      true,
		},
		Node:      &params.Node,
		BKEConfig: params.BKECluster.Name,
		Phase:     bkev1beta1.UpgradeEtcd,
	}

	if params.NeedBackup && params.Node.IP == params.BackupNode.IP {
		upgrade.BackUpEtcd = true
	}

	return upgrade
}

func (e *EnsureEtcdUpgrade) executeUpgradeCommand(upgrade *command.Upgrade,
	node confv1beta1.Node, log *bkev1beta1.BKELogger) error {
	log.Info(constant.EtcdUpgradedReason, "start upgrade etcd %s", phaseutil.NodeInfo(node))

	if err := upgrade.New(); err != nil {
		log.Error(constant.EtcdUpgradeFailed, "upgrade etcd %q failed: %v", phaseutil.NodeInfo(node), err)
		return errors.Errorf("create upgrade command，etcd: %q failed: %v", phaseutil.NodeInfo(node), err)
	}

	return nil
}

func (e *EnsureEtcdUpgrade) waitForUpgradeComplete(params WaitUpgradeParams) error {
	params.Log.Info(constant.EtcdUpgradingReason, "wait upgrade etcd %s finish", phaseutil.NodeInfo(params.Node))

	err, _, failedNodes := params.Upgrade.Wait()
	if err != nil {
		params.Log.Error(constant.EtcdUpgradeFailed, "wait upgrade command complete failed，"+
			"etcd: %q, err: %v", phaseutil.NodeInfo(params.Node), err)
		return errors.Errorf("wait upgrade command complete failed，"+
			"etcd: %q, err: %v", phaseutil.NodeInfo(params.Node), err)
	}

	if len(failedNodes) != 0 {
		params.Log.Error(constant.EtcdUpgradeFailed, "upgrade etcd %q failed: %v",
			phaseutil.NodeInfo(params.Node), err)
		commandErrs, err := phaseutil.LogCommandFailed(*params.Upgrade.Command,
			failedNodes, params.Log, constant.EtcdUpgradingReason)
		phaseutil.MarkNodeStatusByCommandErrs(e.Ctx, e.Ctx.Client, params.BKECluster, commandErrs)
		return errors.Errorf("upgrade etcd %q failed: %v", phaseutil.NodeInfo(params.Node), err)
	}

	params.Log.Info(constant.EtcdUpgradingReason, "upgrade etcd %q operation succeed",
		phaseutil.NodeInfo(params.Node))
	return nil
}

func (e *EnsureEtcdUpgrade) waitForEtcdHealthCheck(params HealthCheckParams) error {
	params.Log.Info(constant.EtcdUpgradingReason, "wait for etcd %q pass healthy check", phaseutil.NodeInfo(params.Node))

	err := wait.PollWithContext(
		params.Ctx,
		EtcdHealthCheckInterval,
		EtcdHealthCheckTimeout,
		func(ctx context.Context) (bool, error) {
			if utils.CtxDone(ctx) {
				return false, nil
			}
			currentVersion, err := e.getEtcdImageVersion(params.Node)
			if err != nil {
				return false, nil
			}
			if !strings.Contains(params.Version, currentVersion) {
				return false, nil
			}
			return true, nil
		})

	if err != nil {
		params.Log.Error(constant.EtcdUpgradeFailed, "upgrade etcd %q failed: %v", phaseutil.NodeInfo(params.Node), err)
		return errors.Errorf("wait for etcd %q pass healthy check failed: %v", phaseutil.NodeInfo(params.Node), err)
	}

	return nil
}

// Extract the image version number from the pod
func (e *EnsureEtcdUpgrade) getEtcdImageVersion(node confv1beta1.Node) (string, error) {
	ctx, c, bkeCluster, _, _ := e.Ctx.Untie()
	clientSet, _, err := kube.GetTargetClusterClient(ctx, c, bkeCluster)
	if err != nil {
		return "", err
	}

	etcdPodName := kube.StaticPodName(mfutil.Etcd, node.Hostname)
	podClient := clientSet.CoreV1().Pods(metav1.NamespaceSystem)

	err = wait.PollImmediate(PollImmeInternal, PollImmeTimeout, func() (bool, error) {
		pod, err := podClient.Get(ctx, etcdPodName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		if pod.Status.Phase != corev1.PodRunning {
			return false, nil
		}

		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return true, nil
			}
		}

		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("failed waiting for etcd pod %s to be ready: %w", etcdPodName, err)
	}

	pod, err := podClient.Get(ctx, etcdPodName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	for _, container := range pod.Spec.Containers {
		if container.Name == "etcd" {
			return extractVersionFromImage(container.Image), nil
		}
	}

	return "", errors.Errorf("etcd container not found in pod %s", etcdPodName)
}

// Extract the version number from the image string.
func extractVersionFromImage(image string) string {
	var version string
	parts := strings.Split(image, ":")
	if len(parts) > 1 {
		version = parts[len(parts)-1]
		return version
	}

	return "unknown"
}

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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	metricrecord "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/metrics/record"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/clusterutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/condition"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
	labelhelper "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/label"
)

const (
	EnsureClusterName confv1beta1.BKEClusterPhase = "EnsureCluster"
	LabelReadyTimeout                             = 10 * time.Second
)

const (
	quickRequeueInterval  = 10 * time.Second
	periodicCheckInterval = 5 * time.Minute
)

type EnsureCluster struct {
	phaseframe.BasePhase
	remoteClient kube.RemoteKubeClient
}

func NewEnsureCluster(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureClusterName)
	return &EnsureCluster{BasePhase: base}
}

func (e *EnsureCluster) Execute() (_ ctrl.Result, err error) {
	var errs []error

	if e.Ctx.Cluster == nil {
		return ctrl.Result{}, errors.Errorf("cluster is nil")
	}

	if !conditions.IsTrue(e.Ctx.Cluster, clusterv1.ControlPlaneInitializedCondition) {
		return ctrl.Result{}, errors.Errorf("cluster is not init")
	}

	if err = e.getRemoteClient(); err != nil {
		errs = append(errs, err)
		return ctrl.Result{}, kerrors.NewAggregate(errs)
	}

	if clusterutil.IsBKECluster(e.Ctx.BKECluster) {
		// ignore error
		if err = e.setAlertLabel(); err != nil {
			errs = append(errs, err)
		}
	}

	if e.Ctx.BKECluster.Spec.ClusterConfig.Cluster.ContainerRuntime.Runtime == "richrunc" {
		// ignore error
		if err = e.setBareMetalLabel(); err != nil {
			errs = append(errs, err)
		}
	}

	// set node label
	if err = e.setNodeLabel(); err != nil {
		errs = append(errs, err)
	}

	if err = e.ensureK8sToken(); err != nil {
		errs = append(errs, err)
	}

	_, _, bkeCluster, _, log := e.Ctx.Untie()
	if err != nil {
		log.Error("some err in ensureCluster.go: %s", err.Error())
		return ctrl.Result{}, kerrors.NewAggregate(errs)
	}

	// 如果集群处于特殊状态，则暂不执行定时检查
	if isClusterInSpecialState(bkeCluster) {
		log.Error("isClusterInSpecialState func err is %s", err.Error())
		return ctrl.Result{}, kerrors.NewAggregate(errs) // 返回聚合错误
	}

	// 后置处理未完成时，不进入健康检查，避免误判为已完成
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	if phaseutil.GetNeedPostProcessNodesWithBKENodes(bkeCluster, bkeNodes).Length() > 0 {
		condition.ConditionMark(bkeCluster, bkev1beta1.NodesPostProcessCondition, confv1beta1.ConditionFalse, constant.NodesPostProcessNotReadyReason, "")
		return ctrl.Result{RequeueAfter: quickRequeueInterval}, errors.Errorf("postprocess not finished")
	}

	if err = e.ensureClusterReady(); err != nil {
		errs = append(errs, err)
		return ctrl.Result{RequeueAfter: quickRequeueInterval}, kerrors.NewAggregate(errs)
	}

	// 正常状态下，定时5min 来检查重新调谐
	return ctrl.Result{RequeueAfter: periodicCheckInterval}, kerrors.NewAggregate(errs)
}

// 校验函数：判断集群是否处于特殊状态
func isClusterInSpecialState(bkeCluster *bkev1beta1.BKECluster) bool {
	// 将相关状态集中在一个数组中进行判断
	specialStates := []confv1beta1.ClusterStatus{
		bkev1beta1.ClusterMasterScalingUp,
		bkev1beta1.ClusterMasterScalingDown,
		bkev1beta1.ClusterWorkerScalingUp,
		bkev1beta1.ClusterWorkerScalingDown,
		bkev1beta1.ClusterInitializing,
		bkev1beta1.ClusterPaused,
		bkev1beta1.ClusterUpgrading,
	}

	// 获取集群的当前状态
	clusterStatus := bkeCluster.Status.ClusterStatus

	// 判断当前状态是否在特殊状态数组中
	for _, status := range specialStates {
		if clusterStatus == status {
			return true
		}
	}
	return false
}

func (e *EnsureCluster) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}
	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

// getRemoteClient get remote cluster client
func (e *EnsureCluster) getRemoteClient() error {
	ctx, c, bkeCluster, _, log := e.Ctx.Untie()
	remoteClient, err := kube.NewRemoteClientByBKECluster(ctx, c, bkeCluster)
	if err != nil {
		log.Error(constant.InternalErrorReason, "failed to get BKECluster %q remote cluster client", utils.ClientObjNS(bkeCluster))
		return err
	}
	e.remoteClient = remoteClient
	e.remoteClient.SetLogger(log.NormalLogger)
	return nil
}

// setAlertLabel set alert label
func (e *EnsureCluster) setAlertLabel() (err error) {

	alterNodeFilter := &metav1.ListOptions{
		LabelSelector: labelhelper.AlertLabelKey,
	}
	alterNode, err := e.remoteClient.ListNodes(alterNodeFilter)
	if err != nil {
		e.Ctx.Log.Error("SetAlertLabelFailed", "failed to list nodes, err: %v", err)
		return errors.Errorf("failed to list nodes, err: %v", err)
	}
	if len(alterNode.Items) > 0 {
		return
	}

	workerNodeFilter := &metav1.ListOptions{
		LabelSelector: labelhelper.NodeRoleNodeLabel,
	}

	nodes, err := e.remoteClient.ListNodes(workerNodeFilter)
	if err != nil {
		e.Ctx.Log.Error("SetAlertLabelFailed", "failed to list nodes, err: %v", err)
		return
	}
	if len(nodes.Items) == 0 {
		e.Ctx.Log.Warn("SetAlertLabelFailed", "(ignore)no worker role node found,skip set alert label")
		return
	}
	availableNode := &nodes.Items[0]
	clientSet, _ := e.remoteClient.KubeClient()
	labelhelper.SetLabel(availableNode, labelhelper.AlertLabelKey, labelhelper.AlertLabelValue)
	_, err = clientSet.CoreV1().Nodes().Update(e.Ctx, availableNode, metav1.UpdateOptions{})
	if err != nil {
		e.Ctx.Log.Warn("SetAlertLabelFailed", "(ignore)failed to set alert label to node %s, err: %v：", availableNode.Name, err)
		return errors.Errorf("failed to set alert label to node %s, err: %v", availableNode.Name, err)
	}
	return
}

func (e *EnsureCluster) setBareMetalLabel() error {
	nodes, err := e.remoteClient.ListNodes(nil)
	if err != nil {
		return err
	}
	clientSet, _ := e.remoteClient.KubeClient()
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if !labelhelper.HasLabel(node, labelhelper.BareMetalLabelKey) {
			labelhelper.SetLabel(node, labelhelper.BareMetalLabelKey, "true")
			if _, err = clientSet.CoreV1().Nodes().Update(e.Ctx, node, metav1.UpdateOptions{}); err != nil {
				e.Ctx.Log.Warn("SetBareMetalLabelFailed", "(ignore)failed to set baremetal label to node %s, err: %v：", node.Name, err)
				continue
			}
		}
	}
	return nil
}

// ensureK8sToken ensure remote cluster k8s token
func (e *EnsureCluster) ensureK8sToken() error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()
	secret, err := phaseutil.GetK8sTokenSecret(ctx, c, bkeCluster)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			token, err := e.remoteClient.NewK8sToken()
			if err = phaseutil.NewK8sTokenSecret(ctx, token, c, bkeCluster); err != nil {
				log.Error(constant.InternalErrorReason, "failed to create BKECluster %q remote cluster k8sToken, err: %v", utils.ClientObjNS(bkeCluster), err)
				return err
			}
			condition.ConditionMark(bkeCluster, "k8sTokenCreated", confv1beta1.ConditionTrue, "k8sTokenCreated", "")
			return nil
		}
		log.Error(constant.InternalErrorReason, "failed to get BKECluster %q remote cluster k8sToken secret, err", utils.ClientObjNS(bkeCluster), err)
		return err
	}
	// add owner reference
	if secret.OwnerReferences == nil || len(secret.OwnerReferences) == 0 {
		if err = controllerutil.SetControllerReference(bkeCluster, secret, scheme); err != nil {
			return err
		} else {
			if err = c.Update(ctx, secret); err != nil {
				return err
			}
		}
	}

	if v, ok := secret.Data["token"]; !ok || string(v) == "" {
		token, err := e.remoteClient.NewK8sToken()
		if err != nil {
			log.Error(constant.InternalErrorReason, "failed to create BKECluster %q remote cluster k8sToken", utils.ClientObjNS(bkeCluster))
			return err
		}
		if err = phaseutil.NewK8sTokenSecret(ctx, token, c, bkeCluster); err != nil {
			log.Error(constant.InternalErrorReason, "failed to create BKECluster %q remote cluster k8sToken", utils.ClientObjNS(bkeCluster))
			return err
		}
	}
	condition.ConditionMark(bkeCluster, "k8sTokenCreated", confv1beta1.ConditionTrue, "k8sTokenCreated", "")
	return nil
}

// ensureRemoteBKEConfigCM
// Deprecated bkeconfig cm will be created before deploy addon cluster-api and bocoperator
func (e *EnsureCluster) ensureRemoteBKEConfigCM() error {
	ctx, c, bkeCluster, _, log := e.Ctx.Untie()

	clientSet, _ := e.remoteClient.KubeClient()
	config, err := phaseutil.GetRemoteBKEConfigCM(ctx, clientSet)
	if err != nil {
		log.Error(constant.InternalErrorReason, "failed to get BKECluster %q remote cluster bke-config cm, err: %v", utils.ClientObjNS(bkeCluster), err)
		return err
	}
	if config == nil {
		if err = phaseutil.MigrateBKEConfigCM(ctx, c, clientSet); err != nil {
			log.Error(constant.InternalErrorReason, "failed to migrate BKECluster %q bke-config cm to remote cluster, err：%v", utils.ClientObjNS(bkeCluster), err)
			return err
		}
	}
	return nil
}

// ensureClusterReady check cluster health status
func (e *EnsureCluster) ensureClusterReady() error {
	ctx, c, bkeCluster, _, log := e.Ctx.Untie()
	// 在首次部署但未完成之前，不检查
	bkeNodes, _ := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(ctx, bkeCluster)
	if bkeCluster.Status.ClusterHealthState == bkev1beta1.Deploying && !phaseutil.ClusterEndDeployedWithContext(ctx, c, e.Ctx.Cluster, bkeCluster, bkeNodes) {
		return errors.Errorf("cluster %s is deploying, can not check health", bkeCluster.Name)
	}

	err := e.runHealthChecks(ctx, c, bkeCluster, log)

	return e.handleClusterReadyPostCheck(bkeCluster, c, log, err)
}

func (e *EnsureCluster) runHealthChecks(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger) error {
	for i := 0; i < 3; i++ {
		if err := e.performHealthCheck(ctx, c, bkeCluster, log); err != nil {
			return err
		}
	}

	log.Finish(constant.ClusterReadyReason, "cluster is ready")
	return nil
}

// handleClusterReadyPostCheck handles operations after ensureClusterReady checks
func (e *EnsureCluster) handleClusterReadyPostCheck(bkeCluster *bkev1beta1.BKECluster, c client.Client, log *bkev1beta1.BKELogger, err error) error {
	metricrecord.ClusterHealthyCountRecord(bkeCluster, bkeCluster.Status.ClusterStatus)

	trackerBkeNodes, trackerErr := e.Ctx.GetBKENodes()
	if trackerErr != nil {
		log.Warn(constant.InternalErrorReason, "failed to get BKENodes for tracker check: %v", trackerErr)
	}
	if phaseutil.ClusterAllowTrackerWithBKENodes(trackerBkeNodes, e.Ctx.Cluster) {
		condition.ConditionMark(bkeCluster, bkev1beta1.TargetClusterReadyCondition, confv1beta1.ConditionTrue, "", "")
	} else {
		condition.ConditionMark(bkeCluster, bkev1beta1.TargetClusterReadyCondition, confv1beta1.ConditionFalse, "", "")
	}
	// 移除追踪器添加的触发调谐的注解
	_, ok := annotation.HasAnnotation(bkeCluster, annotation.ClusterTrackerHealthyCheckFailedAnnotationKey)
	if bkeCluster.Status.ClusterStatus == bkev1beta1.ClusterReady && ok {
		patchFunc := func(combinedBKECluster *bkev1beta1.BKECluster) {
			annotation.RemoveAnnotation(combinedBKECluster, annotation.ClusterTrackerHealthyCheckFailedAnnotationKey)
		}
		return mergecluster.SyncStatusUntilComplete(c, bkeCluster, patchFunc)
	}
	return err
}

// performHealthCheck performs a single health check iteration
func (e *EnsureCluster) performHealthCheck(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, log *bkev1beta1.BKELogger) error {
	rawNodes, err := e.Ctx.NodeFetcher().GetBKENodesForBKECluster(e.Ctx.Context, bkeCluster)
	if err != nil {
		log.Error(constant.InternalErrorReason, "failed to get nodes for cluster health check: %v", err)
		return err
	}
	bkeNodes := bkev1beta1.NewBKENodes(rawNodes)
	if err := e.remoteClient.CheckClusterHealth(bkeCluster, bkeCluster.Status.KubernetesVersion, bkeNodes); err != nil {
		bkeCluster.Status.ClusterStatus = bkev1beta1.ClusterUnhealthy
		bkeCluster.Status.ClusterHealthState = bkev1beta1.Unhealthy
		log.Warn(constant.ClusterUnhealthyReason, err.Error())
		log.Error("ensureCluster CheckClusterHealth func err is %s", err.Error())

		if updateErr := mergecluster.UpdateModifiedBKENodes(ctx, c, bkeNodes); updateErr != nil {
			log.Warn(constant.InternalErrorReason, "Failed to update BKENode status: %v", updateErr)
		}
		return err
	}

	// Update BKENode status after successful health check
	if err := mergecluster.UpdateModifiedBKENodes(ctx, c, bkeNodes); err != nil {
		log.Warn(constant.InternalErrorReason, "Failed to update BKENode status: %v", err)
	}

	e.updateClusterVersionStatus(bkeCluster)

	bkeCluster.Status.ClusterStatus = bkev1beta1.ClusterReady
	bkeCluster.Status.ClusterHealthState = bkev1beta1.Healthy

	if err := e.Report("", false); err != nil {
		log.Error("ensureCluster err is %s", err.Error())
		return err
	}
	return nil
}

// updateClusterVersionStatus updates version fields in cluster status if empty
func (e *EnsureCluster) updateClusterVersionStatus(bkeCluster *bkev1beta1.BKECluster) {
	if bkeCluster.Status.KubernetesVersion == "" {
		bkeCluster.Status.KubernetesVersion = bkeCluster.Spec.ClusterConfig.Cluster.KubernetesVersion
	}
	if bkeCluster.Status.EtcdVersion == "" {
		bkeCluster.Status.EtcdVersion = bkeCluster.Spec.ClusterConfig.Cluster.EtcdVersion
	}
	if bkeCluster.Status.OpenFuyaoVersion == "" {
		bkeCluster.Status.OpenFuyaoVersion = bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion
	}
	if bkeCluster.Status.ContainerdVersion == "" {
		bkeCluster.Status.ContainerdVersion = bkeCluster.Spec.ClusterConfig.Cluster.ContainerdVersion
	}
}

// ensureAgentStatus check bkeagent status
func (e *EnsureCluster) ensureAgentStatus() error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()
	if condition.HasConditionStatus(bkev1beta1.SwitchBKEAgentCondition, bkeCluster, confv1beta1.ConditionTrue) {
		log.Info(constant.BKEAgentUnknownReason, "unknown bkeagent status, already switch bkeagent, skip check bkeagent status")
		return nil
	}

	if bkeCluster.Status.AgentStatus.Replies != 0 {
		err, _, failedNodes := phaseutil.PingBKEAgent(ctx, c, scheme, bkeCluster)
		if err != nil {
			log.Error(constant.BKEAgentNotReadyReason, "Failed to ping BKEAgent, err: %v", err)
		}

		if len(failedNodes) != 0 {
			errInfo := fmt.Sprintf("Failed to ping bkeagent on flow Nodes: %v", failedNodes)
			log.Error(constant.BKEAgentNotReadyReason, errInfo)
			return errors.New(errInfo)
		}
		log.Info(constant.BKEAgentReadyReason, "BKEAgent is ready")
		return nil
	}
	return nil
}

// setNodeLabel is the main function that coordinates the process of setting node labels.
func (e *EnsureCluster) setNodeLabel() error {
	globalLabels := e.Ctx.BKECluster.Spec.ClusterConfig.Cluster.Labels
	allNodes, err := e.Ctx.GetNodes()
	if err != nil {
		return fmt.Errorf("failed to get nodes: %v", err)
	}
	nodes, err := e.remoteClient.ListNodes(nil)
	if err != nil {
		e.Ctx.Log.Error("GetNodeLabelFailed", "failed to list nodes, err: %v", err)
		return fmt.Errorf("failed to list nodes: %v", err)
	}

	setNodeLablesMap := e.buildNodeLabelsMap(globalLabels, allNodes)

	clientSet, _ := e.remoteClient.KubeClient()
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if err = e.applyLabelsToNode(clientSet, node, setNodeLablesMap); err != nil {
			return err
		}
	}

	return nil
}

// buildNodeLabelsMap builds a map of node labels to apply, merging global labels.
func (e *EnsureCluster) buildNodeLabelsMap(
	globalLabels []confv1beta1.Label,
	setNodeLabels []confv1beta1.Node,
) map[string]map[string]string {
	setNodeLablesMap := make(map[string]map[string]string)
	for _, node := range setNodeLabels {
		labelMap := mergeLabels(node.Labels, globalLabels)
		if len(labelMap) > 0 {
			setNodeLablesMap[node.Hostname] = labelMap
		}
	}
	return setNodeLablesMap
}

// mergeLabels merges the node-specific labels with the global labels.
func mergeLabels(nodeLabels []confv1beta1.Label, globalLabels []confv1beta1.Label) map[string]string {
	labelMap := make(map[string]string)
	for _, label := range nodeLabels {
		labelMap[label.Key] = label.Value
	}
	for _, label := range globalLabels {
		if _, ok := labelMap[label.Key]; !ok {
			labelMap[label.Key] = label.Value
		}
	}
	return labelMap
}

// applyLabelsToNode applies the labels to a given node if necessary.
func (e *EnsureCluster) applyLabelsToNode(
	clientSet *kubernetes.Clientset,
	node *v1.Node,
	setNodeLabelsMap map[string]map[string]string,
) error {
	labels, found := getNodeLabels(node.Name, setNodeLabelsMap)
	if !found {
		return nil
	}
	return e.applyNecessaryLabels(clientSet, node, labels)
}

// getNodeLabels retrieves the labels for a given node from the label map.
func getNodeLabels(nodeName string, setNodeLabelsMap map[string]map[string]string) (map[string]string, bool) {
	labels, ok := setNodeLabelsMap[nodeName]
	return labels, ok
}

// applyNecessaryLabels checks and applies labels to the node if necessary.
func (e *EnsureCluster) applyNecessaryLabels(
	clientSet *kubernetes.Clientset,
	node *v1.Node,
	labels map[string]string,
) error {
	for key, value := range labels {
		if !labelhelper.IsLabelEqual(node, key, value) {
			if err := e.waitLabelReady(clientSet, node, key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *EnsureCluster) waitLabelReady(clientSet *kubernetes.Clientset, node *v1.Node, k, v string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	err := wait.PollImmediateUntil(LabelReadyTimeout, func() (bool, error) {
		// Get the latest version of the node
		latestNode, err := clientSet.CoreV1().Nodes().Get(e.Ctx, node.Name, metav1.GetOptions{})
		if err != nil {
			// Log the error and retry
			e.Ctx.Log.Error("GetNodeError", "failed to get node %s: %v", node.Name, err)
			return false, errors.Errorf("failed to get node %s: %v", node.Name, err)
		}

		// Create a deep copy of the node to avoid modifying the original object
		nodeCopy := latestNode.DeepCopy()

		// Set the label on the latest node copy
		labelhelper.SetLabel(nodeCopy, k, v)

		// Try to update the node with the latest version
		_, err = clientSet.CoreV1().Nodes().Update(e.Ctx, nodeCopy, metav1.UpdateOptions{
			FieldManager: "node-label-updater",
		})

		if err != nil {
			e.Ctx.Log.Error("SetNodeLabelConFailed", "failed to set label %s=%s on node %s, error: %v", k, v, node.Name, err)
			return false, nil
		}

		e.Ctx.Log.Info("SetNodeLabelSuccess", "Successfully set label %s=%s on node %s", k, v, node.Name)
		return true, nil // Success

	}, ctx.Done())

	if err != nil {
		return errors.Errorf("failed to set label %s=%s on node %s after retries: %v", k, v, node.Name, err)
	}

	return nil
}

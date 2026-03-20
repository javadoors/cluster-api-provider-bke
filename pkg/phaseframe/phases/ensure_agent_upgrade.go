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
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/go-semver/semver"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/command"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	// EnsureAgentUpgradeName 升级名称
	EnsureAgentUpgradeName        confv1beta1.BKEClusterPhase = "EnsureAgentUpgrade"
	bkeagentDeployerName                                      = "bkeagent-deployer"
	bkeagentDeployerNamespace                                 = "cluster-system"
	bkeagentDeployerContainerName                             = "deployer"
	// DaemonsetReadyTimeout 等待Daemonset就绪时间
	DaemonsetReadyTimeout = 5 * time.Minute
)

// EnsureAgentUpgrade 结构体
type EnsureAgentUpgrade struct {
	phaseframe.BasePhase
	remoteClient *kubernetes.Clientset
	targetImage  string
}

// NewEnsureAgentUpgrade 构造函数
func NewEnsureAgentUpgrade(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureAgentUpgradeName)
	return &EnsureAgentUpgrade{BasePhase: base}
}

// NeedExecute 判断是否需要执行
func (e *EnsureAgentUpgrade) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}

	// 1. 检查 spec 和 status 是否有差异（openFuyaoVersion 变化）
	if new.Status.OpenFuyaoVersion == new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion {
		e.SetStatus(bkev1beta1.PhaseSucceeded)
		return false
	}

	// 2. 从 spec 中解析出 bkeagent-deployer 的目标版本（不访问远程）
	targetVersion, err := e.getTargetBKEAgentDeployerVersionFromSpec(new)
	if err != nil {
		// 无法解析目标版本，保守起见不执行（避免误升级）
		e.SetStatus(bkev1beta1.PhaseSucceeded)
		return false
	}

	// 3. 从 status.addonStatus 中获取当前已部署的 bkeagent-deployer 版本
	currentVersion := e.getCurrentBKEAgentDeployerVersionFromStatus(new)
	afterVersion := strings.TrimPrefix(currentVersion, "v")

	// 4. 如果当前版本已是目标版本，跳过
	if (currentVersion == "") || (afterVersion == targetVersion) {
		e.SetStatus(bkev1beta1.PhaseSucceeded)
		return false
	}

	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

func (e *EnsureAgentUpgrade) getTargetBKEAgentDeployerVersionFromSpec(
	bkeCluster *bkev1beta1.BKECluster) (string, error) {
	// 复用已有的逻辑，但只解析版本，不获取远程 client
	patchCfg, err := e.GetPatchConfig(bkeCluster)
	if err != nil {
		return "", err
	}

	// 找到 bkeagent-deployer 镜像，提取 tag 作为版本
	for _, repo := range patchCfg.Repos {
		for _, subImage := range repo.SubImages {
			if version, found := e.findBKEAgentDeployerVersionInSubImage(subImage); found {
				return version, nil
			}
		}
	}
	return "", fmt.Errorf("bkeagent-deployer version not found in patch config")
}

// findBKEAgentDeployerVersionInSubImage 在 SubImage 中查找 bkeagent-deployer 镜像的 tag
func (e *EnsureAgentUpgrade) findBKEAgentDeployerVersionInSubImage(
	subImage phaseutil.SubImage) (string, bool) {
	for _, image := range subImage.Images {
		if e.isAgentDeployerImage(image) && len(image.Tag) > 0 {
			return image.Tag[0], true
		}
	}
	return "", false
}

func (e *EnsureAgentUpgrade) getCurrentBKEAgentDeployerVersionFromStatus(bkeCluster *bkev1beta1.BKECluster) string {
	for _, addon := range bkeCluster.Status.AddonStatus {
		if addon.Name == "bkeagent-deployer" {
			return addon.Version
		}
	}
	return "" // 未部署过
}

func (e *EnsureAgentUpgrade) getRemoteClient(bkeCluster *bkev1beta1.BKECluster) error {
	if e.remoteClient != nil {
		return nil
	}
	ctx, c, _, _, log := e.Ctx.Untie()

	clientSet, _, err := kube.GetTargetClusterClient(ctx, c, bkeCluster)
	if err != nil {
		log.Error(constant.AgentUpgradeFailed, "failed to get target cluster clientset: %v", err)
		return err
	}
	e.remoteClient = clientSet
	return nil
}

// DaemonsetTarget 结构体
type DaemonsetTarget struct {
	Namespace string
	Name      string
	Container string
}

// GetDaemonsetImage 读取指定 Daemonset 容器的当前镜像
func GetDaemonsetImage(ctx context.Context, clientSet *kubernetes.Clientset, target DaemonsetTarget) (string, error) {
	daemon, err := clientSet.AppsV1().DaemonSets(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get Daemonset %s/%s: %w", target.Namespace, target.Name, err)
	}
	for _, c := range daemon.Spec.Template.Spec.Containers {
		if c.Name == target.Container {
			return c.Image, nil
		}
	}
	return "", fmt.Errorf("container %s not found", target.Container)
}

// getAgentDeployerTargetImage 从 PatchConfig 中获取 bkeagent-deployer 目标镜像
func (e *EnsureAgentUpgrade) getAgentDeployerTargetImage(bkeCluster *bkev1beta1.BKECluster) (string, error) {
	_, _, _, _, log := e.Ctx.Untie()

	patchCfg, err := e.GetPatchConfig(bkeCluster)
	if err != nil {
		return "", err
	}

	fullImage, err := e.FindAgentDeployerImageInPatchConfig(patchCfg)
	if err != nil {
		return "", err
	}

	log.Info(constant.AgentUpgradingReason, "cannot find bkeagent-deployer image: %s", fullImage)
	return fullImage, nil
}

// FindProviderImageInPatchConfig 从 PatchConfig 中查找 bkeagent-deployer 镜像
func (e *EnsureAgentUpgrade) FindAgentDeployerImageInPatchConfig(patchCfg *phaseutil.PatchConfig) (string, error) {
	for _, repo := range patchCfg.Repos {
		for _, subImage := range repo.SubImages {
			if image, found := e.findAgentDeployerImageInSubImage(subImage); found {
				return image, nil
			}
		}
	}
	return "", fmt.Errorf("cannot find bkeagent-deployer image in patch file")
}

// findAgentDeployerImageInSubImage 在 SubImage 中查找 bkeagent-deployer 镜像
func (e *EnsureAgentUpgrade) findAgentDeployerImageInSubImage(subImage phaseutil.SubImage) (string, bool) {
	for _, image := range subImage.Images {
		if e.isAgentDeployerImage(image) {
			if len(image.Tag) == 0 {
				continue
			}
			deployerImage := fmt.Sprintf("%s/%s:%s",
				strings.TrimSuffix(subImage.SourceRepo, "/"),
				strings.TrimPrefix(image.Name, "/"),
				image.Tag[0])
			return deployerImage, true
		}
	}
	return "", false
}

// isAgentDeployerImage 判断是否为 AgentDeployer 镜像
func (e *EnsureAgentUpgrade) isAgentDeployerImage(image phaseutil.Image) bool {
	// 通过镜像名匹配
	if strings.Contains(image.Name, "bkeagent-deployer") {
		return true
	}

	// 通过 PodInfo 匹配
	for _, podInfo := range image.UsedPodInfo {
		if podInfo.PodPrefix == bkeagentDeployerName && podInfo.NameSpace == bkeagentDeployerNamespace {
			return true
		}
	}

	return false
}

// GetPatchConfig 获取 patch 配置
func (e *EnsureAgentUpgrade) GetPatchConfig(bkeCluster *bkev1beta1.BKECluster) (*phaseutil.PatchConfig, error) {
	ctx, c, _, _, log := e.Ctx.Untie()
	openFuyaoVersion := bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion
	log.Info(constant.AgentUpgradingReason, "openFuyaoVersion: %v", openFuyaoVersion)

	bkeCmKey := fmt.Sprintf("patch.%s", openFuyaoVersion)
	patchCmKey := fmt.Sprintf("cm.%s", openFuyaoVersion)

	localConfigMap := &corev1.ConfigMap{}
	if err := c.Get(ctx, constant.GetLocalConfigMapObjectKey(), localConfigMap); err != nil {
		log.Error(constant.AgentUpgradingReason, "failed to get local cluster bke-config cm, err: %v", err)
		return nil, fmt.Errorf("get cm failed %v", err)
	}

	// 检查 patch.<version> key 是否存在
	if _, ok := localConfigMap.Data[bkeCmKey]; !ok {
		log.Info(constant.AgentUpgradingReason, "patch configuration  %s do not exist，skip", bkeCmKey)
		return nil, fmt.Errorf("patch info %s not found (non-patch version)", bkeCmKey)
	}

	// 读取 openfuyao-patch/cm.<version> ConfigMap
	cmKey := client.ObjectKey{
		Namespace: "openfuyao-patch",
		Name:      patchCmKey,
	}
	patchConfigMap := &corev1.ConfigMap{}
	if err := c.Get(ctx, cmKey, patchConfigMap); err != nil {
		log.Error(constant.AgentUpgradingReason, "failed to get patch cm, err: %v", err)
		return nil, fmt.Errorf("get cm failed %v", err)
	}

	// 解析 yaml 配置
	if _, ok := patchConfigMap.Data[openFuyaoVersion]; !ok {
		return nil, fmt.Errorf("patch info %s not found in patch config", openFuyaoVersion)
	}

	log.Info(constant.AgentUpgradingReason, "get patch config data length: %d", len(patchConfigMap.Data[openFuyaoVersion]))
	return phaseutil.GetPatchConfig(patchConfigMap.Data[openFuyaoVersion])
}

// getBKEAgentDeployerTarget 返回 BKEAgentDeployer 目标信息
func getBKEAgentDeployerTarget() DaemonsetTarget {
	return DaemonsetTarget{
		Namespace: bkeagentDeployerNamespace,
		Name:      bkeagentDeployerName,
		Container: bkeagentDeployerContainerName,
	}
}

// isBKEAgentDeployerNeedUpgrade 判断bkeagent-deployer是否需要升级
func (e *EnsureAgentUpgrade) isBKEAgentDeployerNeedUpgrade(old *bkev1beta1.BKECluster,
	new *bkev1beta1.BKECluster) bool {

	ctx, _, _, _, log := e.Ctx.Untie()

	// 非初次安装，版本未变化则跳过
	if new.Status.OpenFuyaoVersion == new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion {
		log.Debug(constant.AgentUpgradingReason, "version is not change")
		return false
	}

	target := getBKEAgentDeployerTarget()
	currentImage, err := GetDaemonsetImage(ctx, e.remoteClient, target)
	if err != nil {
		log.Error(constant.AgentUpgradeFailed, "read now daemonset image failed， err: %v", err)
	}

	targetImage, err := e.getAgentDeployerTargetImage(new)

	if err != nil {
		log.Info(constant.AgentUpgradingReason, "cannot find bkeagent-deployer image，skip update, err: %v", err)
		return false
	}

	if targetImage == "" {
		log.Info(constant.AgentUpgradingReason, "target image is null，skip update")
		return false
	}

	e.targetImage = targetImage
	if currentImage == targetImage {
		log.Info(constant.AgentUpgradingReason, "this image is already target image，skip update, "+
			"image: %s", currentImage)
		return false
	}

	log.Info(constant.AgentUpgradingReason, "detect image is not same, need update, "+
		"current: %s, target: %s", currentImage, targetImage)
	return true
}

func (e *EnsureAgentUpgrade) isPatchVersion(version string) bool {
	cleanVersion := strings.TrimPrefix(version, "v")
	v, err := semver.NewVersion(cleanVersion)
	if err != nil {
		return false
	}
	return v.Patch > 0 && v.PreRelease == ""
}

// Execute 执行bkeagent-deployer升级操作
func (e *EnsureAgentUpgrade) Execute() (ctrl.Result, error) {
	_, _, bkeCluster, _, _ := e.Ctx.Untie()
	if err := e.getRemoteClient(bkeCluster); err != nil {
		return ctrl.Result{}, err
	}
	if !e.isBKEAgentDeployerNeedUpgrade(nil, bkeCluster) {
		// 如果不需要，就跳过升级（但 NeedExecute 已返回 true，所以这里只是安全兜底）
		return ctrl.Result{}, nil
	}
	return e.upgradeBKEAgentDeployer()
}

// getBKEAgentDeployerVersionFromCluster 从集群中查询bkeagent-deployer的实际运行版本
func (e *EnsureAgentUpgrade) getBKEAgentDeployerVersionFromCluster() (string, error) {
	ctx, _, _, _, _ := e.Ctx.Untie()

	// 查询bkeagent-deployer DaemonSet
	daemonSet, err := e.remoteClient.AppsV1().DaemonSets(bkeagentDeployerNamespace).
		Get(ctx, bkeagentDeployerName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("bkeagent-deployer DaemonSet not found")
		}
		return "", err
	}

	// 优先从Label中获取版本
	if version, ok := daemonSet.Labels["version"]; ok {
		return version, nil
	}
	if version, ok := daemonSet.Labels["app.kubernetes.io/version"]; ok {
		return version, nil
	}

	// 从镜像标签中提取版本
	if len(daemonSet.Spec.Template.Spec.Containers) > 0 {
		image := daemonSet.Spec.Template.Spec.Containers[0].Image
		if version := e.extractVersionFromImage(image); version != "" {
			return version, nil
		}
	}

	return "", fmt.Errorf("cannot extract version from bkeagent-deployer DaemonSet")
}

// extractVersionFromImage 从镜像名称中提取版本号
// 例如: registry.example.com/bkeagent-deployer:v1.2.3 -> v1.2.3
func (e *EnsureAgentUpgrade) extractVersionFromImage(image string) string {
	parts := strings.Split(image, ":")
	const length = 2
	if len(parts) == length {
		return parts[1]
	}
	return ""
}

// PatchDaemonsetImage 更新 Daemonset 指定容器的镜像
func PatchDaemonsetImage(ctx context.Context, clientSet *kubernetes.Clientset,
	target DaemonsetTarget, image string) error {
	daemon, err := clientSet.AppsV1().DaemonSets(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get Daemonset: %w", err)
	}

	// 深拷贝避免修改原始对象（client-go 要求）
	updatedDaemon := daemon.DeepCopy()
	updated := false
	for i := range updatedDaemon.Spec.Template.Spec.Containers {
		if updatedDaemon.Spec.Template.Spec.Containers[i].Name == target.Container {
			updatedDaemon.Spec.Template.Spec.Containers[i].Image = image
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("container %s not found", target.Container)
	}

	if updatedDaemon.Spec.Template.Annotations == nil {
		updatedDaemon.Spec.Template.Annotations = make(map[string]string)
	}
	updatedDaemon.Spec.Template.Annotations["bke.openfuyao.cn/restartedAt"] = time.Now().Format(time.RFC3339)

	_, err = clientSet.AppsV1().DaemonSets(target.Namespace).Update(ctx, updatedDaemon, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update Daemonset: %w", err)
	}
	return nil
}

// WaitDaemonsetReady 等待 DaemonSet 所有 Pod 就绪且使用目标镜像
func WaitDaemonsetReady(ctx context.Context, clientSet *kubernetes.Clientset,
	target DaemonsetTarget, targetImage string, timeout time.Duration) error {
	const length = 2
	pollInterval := length * time.Second
	return wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		ds, err := clientSet.AppsV1().DaemonSets(target.Namespace).Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if ds.Status.DesiredNumberScheduled == 0 ||
			ds.Status.UpdatedNumberScheduled != ds.Status.DesiredNumberScheduled ||
			ds.Status.NumberUnavailable != 0 {
			return false, nil
		}

		selector := labels.SelectorFromSet(ds.Spec.Selector.MatchLabels)
		podList, err := clientSet.CoreV1().Pods(target.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: selector.String(),
		})
		if err != nil {
			return false, err
		}

		if len(podList.Items) == 0 {
			return false, nil
		}

		for _, pod := range podList.Items {
			if !podHasImage(pod, targetImage) || !phaseutil.PodIsReady(pod) {
				return false, nil
			}
		}
		return true, nil
	})
}

// podHasImage 检查 Pod 是否使用目标镜像
func podHasImage(pod corev1.Pod, targetImage string) bool {
	for _, container := range pod.Spec.Containers {
		if container.Image == targetImage {
			return true
		}
	}
	return false
}

func (e *EnsureAgentUpgrade) upgradeBKEAgentDeployer() (ctrl.Result, error) {
	ctx, _, bkeCluster, _, log := e.Ctx.Untie()
	target := getBKEAgentDeployerTarget()

	targetImage, err := e.getAgentDeployerTargetImage(bkeCluster)
	if err != nil || targetImage == "" {
		errMsg := fmt.Sprintf("无法解析目标镜像: %v", err)
		log.Error(constant.AgentUpgradeFailed, errMsg)
		return ctrl.Result{}, fmt.Errorf("无法解析目标镜像: %v", err)
	}

	log.Info(constant.AgentUpgradingReason, "start patch daemonset image, target: %s", targetImage)
	if err := PatchDaemonsetImage(ctx, e.remoteClient, target, targetImage); err != nil {
		errMsg := fmt.Sprintf("patch daemonset 失败: %v", err)
		log.Error(constant.AgentUpgradeFailed, errMsg)
		return ctrl.Result{}, fmt.Errorf("patch daemonset 失败: %v", err)
	}

	log.Info(constant.AgentUpgradingReason, "waiting  Pod ready...")
	if err := WaitDaemonsetReady(ctx, e.remoteClient, target, targetImage, DaemonsetReadyTimeout); err != nil {
		// 检查是否为 context canceled 且镜像已更新
		if strings.Contains(err.Error(), "context canceled") {
			currentImage, getErr := GetDaemonsetImage(context.Background(), e.remoteClient, target)
			if getErr == nil && currentImage == targetImage {
				log.Info(constant.AgentUpgradeSuccess, "detect image change，update ok")
				return ctrl.Result{Requeue: true}, nil
			}
		}

		errMsg := fmt.Sprintf("等待 Daemonset 就绪失败: %v", err)
		log.Error(constant.AgentUpgradeFailed, errMsg)
		return ctrl.Result{}, fmt.Errorf("等待 Daemonset 就绪失败: %v", err)
	}

	log.Info("BKEAgentUpgrade", "bkeagent-deployer DaemonSet image updated successfully")

	// 步骤2: 下发bkeagent指令到节点
	if err := e.sendBKEAgentCommand(); err != nil {
		log.Error("BKEAgentUpgradeFailed", "failed to send bkeagent command: %v", err)
		return ctrl.Result{}, err
	}

	log.Info("BKEAgentUpgrade", "bkeagent command sent successfully")

	// 升级成功后，更新 AddonStatus
	targetVersion := e.extractVersionFromImage(targetImage)
	if err := e.updateBKEAgentDeployerAddonStatus(bkeCluster, targetVersion); err != nil {
		log.Error("BKEAgentUpgradeFailed", "failed to update addon status: %v", err)
		return ctrl.Result{}, err
	}

	log.Info("BKEAgentUpgradeSuccess", "bkeagent-deployer upgrade completed")
	return ctrl.Result{}, nil
}

func (e *EnsureAgentUpgrade) updateBKEAgentDeployerAddonStatus(bkeCluster *bkev1beta1.BKECluster,
	version string) error {
	_, c, _, _, _ := e.Ctx.Untie()

	// 查找或添加 bkeagent-deployer 到 AddonStatus
	found := false
	for i := range bkeCluster.Status.AddonStatus {
		if bkeCluster.Status.AddonStatus[i].Name == "bkeagent-deployer" {
			bkeCluster.Status.AddonStatus[i].Version = version
			found = true
			break
		}
	}
	if !found {
		bkeCluster.Status.AddonStatus = append(bkeCluster.Status.AddonStatus, confv1beta1.Product{
			Name:    "bkeagent-deployer",
			Version: version,
		})
	}

	return mergecluster.SyncStatusUntilComplete(c, bkeCluster)
}

// sendBKEAgentCommand 下发bkeagent指令到节点
func (e *EnsureAgentUpgrade) sendBKEAgentCommand() error {
	ctx, c, bkeCluster, scheme, log := e.Ctx.Untie()
	// Use NodeFetcher to get BKENodes from API server
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
	if err != nil {
		log.Warn("BKEAgentUpgrade", "failed to get BKENodes: %v", err)
		return nil
	}
	nodes := phaseutil.GetAgentPushedNodesWithBKENodes(bkeNodes)
	if nodes.Length() == 0 {
		log.Warn("BKEAgentUpgrade", "no nodes with agent pushed, skip sending command")
		return nil
	}
	timeOut, err := phaseutil.GetBootTimeOut(bkeCluster)
	if err != nil {
		log.Warn("BKEAgentUpgrade", "Get boot timeout failed. err: %v", err)
		timeOut = command.DefaultWaitTimeout
	}
	commandSpec := command.GenerateDefaultCommandSpec()
	commandSpec.Commands = []agentv1beta1.ExecCommand{
		{ID: "rolloutBKEAgent",
			Command:       []string{"SelfUpdate"},
			Type:          agentv1beta1.CommandBuiltIn,
			BackoffIgnore: false,
		},
	}
	customCommand := command.Custom{
		BaseCommand: command.BaseCommand{
			Ctx: ctx, NameSpace: bkeCluster.Namespace,
			Client: c, Scheme: scheme,
			OwnerObj: bkeCluster, ClusterName: bkeCluster.Name,
			Unique: true, RemoveAfterWait: true, WaitTimeout: timeOut,
		},
		Nodes: nodes, CommandName: "bkeagent-deployer-upgrade",
		CommandSpec: commandSpec, CommandLabel: command.BKEClusterLabel,
	}
	if err := customCommand.New(); err != nil {
		return fmt.Errorf("failed to create bkeagent command: %v", err)
	}
	log.Info("BKEAgentUpgrade", "waiting for bkeagent command to complete on %d nodes", nodes.Length())
	err, successNodes, failedNodes := customCommand.Wait()
	if err != nil {
		errInfo := fmt.Sprintf("bkeagent command execution failed: %d/%d nodes succeeded",
			len(successNodes), len(successNodes)+len(failedNodes))
		log.Error("BKEAgentUpgradeFailed", errInfo)
		return fmt.Errorf("%s: %v", errInfo, err)
	}
	if len(failedNodes) > 0 {
		errInfo := fmt.Sprintf("bkeagent command failed on %d nodes: %v", len(failedNodes), failedNodes)
		log.Error("BKEAgentUpgradeFailed", errInfo)
		return fmt.Errorf("bkeagent command failed on %d nodes: %v", len(failedNodes), failedNodes)
	}
	log.Info("BKEAgentUpgrade", "bkeagent command executed successfully on all %d nodes", len(successNodes))
	return nil
}

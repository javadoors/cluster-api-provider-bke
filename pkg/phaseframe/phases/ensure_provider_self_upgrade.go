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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	EnsureProviderSelfUpgradeName confv1beta1.BKEClusterPhase = "EnsureProviderSelfUpgrade"
	providerNamespace                                         = "cluster-system"
	providerDeploymentName                                    = "bke-controller-manager"
	providerContainerName                                     = "manager"
	providerImageName                                         = "cluster-api-provider-bke"

	deploymentReadyTimeout   = 5 * time.Minute
	gracefulShutdownDuration = 2 * time.Second
)

type EnsureProviderSelfUpgrade struct {
	phaseframe.BasePhase
}

func NewEnsureProviderSelfUpgrade(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureProviderSelfUpgradeName)
	return &EnsureProviderSelfUpgrade{BasePhase: base}
}

func getProviderDeploymentTarget() phaseutil.DeploymentTarget {
	return phaseutil.DeploymentTarget{
		Namespace: providerNamespace,
		Name:      providerDeploymentName,
		Container: providerContainerName,
	}
}

func (p *EnsureProviderSelfUpgrade) NeedExecute(old, new *bkev1beta1.BKECluster) bool {
	if !p.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}

	if !p.isProviderNeedUpgrade(old, new) {
		return false
	}

	p.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

func (p *EnsureProviderSelfUpgrade) isProviderNeedUpgrade(old, new *bkev1beta1.BKECluster) bool {
	ctx, c, _, _, log := p.Ctx.Untie()

	// First installation (Status.OpenFuyaoVersion is empty)
	if new.Status.OpenFuyaoVersion == "" {
		if !p.isPatchVersion(new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion) {
			log.Debug(constant.ProviderSelfUpgradeReason, "first installation with non-patch version, skip self-upgrade")
			return false
		}
	} else {
		// Not first installation, skip if version unchanged
		if new.Status.OpenFuyaoVersion == new.Spec.ClusterConfig.Cluster.OpenFuyaoVersion {
			log.Debug(constant.ProviderSelfUpgradeReason, "provider-bke version unchanged")
			return false
		}
	}

	// Check Deployment image first
	target := getProviderDeploymentTarget()
	currentImage, err := phaseutil.GetDeploymentImage(ctx, c, target)
	if err != nil {
		log.Error(constant.ProviderSelfUpgradeFailed, "failed to get current Deployment image, err: %v", err)
		return false
	}

	targetImage, err := p.getProviderTargetImage(new)
	if err != nil {
		log.Info(constant.ProviderSelfUpgradeReason, "unable to parse provider target image, skip self-upgrade, err: %v", err)
		return false
	}

	if targetImage == "" {
		log.Info(constant.ProviderSelfUpgradeReason, "target image is empty, skip self-upgrade")
		return false
	}

	if currentImage == targetImage {
		log.Info(constant.ProviderSelfUpgradeReason, "current image is already target image, skip self-upgrade, image: %s", currentImage)
		return false
	}

	log.Info(constant.ProviderSelfUpgradeReason, "detected image mismatch, need self-upgrade, current: %s, target: %s", currentImage, targetImage)
	return true
}

func (p *EnsureProviderSelfUpgrade) isPatchVersion(version string) bool {
	cleanVersion := strings.TrimPrefix(version, "v")
	v, err := semver.NewVersion(cleanVersion)
	if err != nil {
		return false
	}
	return v.Patch > 0 && v.PreRelease == ""
}

func (p *EnsureProviderSelfUpgrade) Execute() (ctrl.Result, error) {
	return p.rolloutProvider()
}

func (p *EnsureProviderSelfUpgrade) rolloutProvider() (ctrl.Result, error) {
	ctx, c, bkeCluster, _, log := p.Ctx.Untie()
	target := getProviderDeploymentTarget()

	targetImage, err := p.getProviderTargetImage(bkeCluster)
	if err != nil || targetImage == "" {
		log.Error(constant.ProviderSelfUpgradeFailed, "unable to parse target image: %v", err)
		return ctrl.Result{}, fmt.Errorf("unable to parse target image: %w", err)
	}

	log.Info(constant.ProviderSelfUpgradeReason, "start patching Deployment image, target: %s", targetImage)
	if err := phaseutil.PatchDeploymentImage(ctx, c, target, targetImage); err != nil {
		log.Error(constant.ProviderSelfUpgradeFailed, "patch Deployment failed: %v", err)
		return ctrl.Result{}, fmt.Errorf("patch Deployment failed: %w", err)
	}

	log.Info(constant.ProviderSelfUpgradeReason, "waiting for new version Pod to be ready...")
	if err := phaseutil.WaitDeploymentReady(ctx, c, target, targetImage, deploymentReadyTimeout); err != nil {
		// Check if context canceled but image is updated
		if strings.Contains(err.Error(), "context canceled") {
			currentImage, getErr := phaseutil.GetDeploymentImage(context.Background(), c, target)
			if getErr == nil && currentImage == targetImage {
				log.Info(constant.ProviderSelfUpgradeSuccess, "detected context canceled but image is updated, consider self-upgrade successful")
				return ctrl.Result{Requeue: true}, nil
			}
		}

		log.Error(constant.ProviderSelfUpgradeFailed, "wait for Deployment ready failed: %v", err)
		return ctrl.Result{}, fmt.Errorf("wait for Deployment ready failed: %w", err)
	}

	log.Info(constant.ProviderSelfUpgradeSuccess, "provider self-upgrade completed")
	return ctrl.Result{Requeue: true}, nil
}

func (p *EnsureProviderSelfUpgrade) PostHook(err error) error {
	if hookErr := p.DefaultPostHook(err); hookErr != nil {
		return hookErr
	}

	if err == nil {
		_, _, _, _, log := p.Ctx.Untie()
		log.Info(constant.ProviderSelfUpgradeSuccess, "self-upgrade successful")
		time.Sleep(gracefulShutdownDuration)
	}

	return nil
}

func (p *EnsureProviderSelfUpgrade) getProviderTargetImage(bkeCluster *bkev1beta1.BKECluster) (string, error) {
	_, _, _, _, log := p.Ctx.Untie()

	patchCfg, err := p.getPatchConfig(bkeCluster)
	if err != nil {
		return "", err
	}

	fullImage, err := p.findProviderImageInPatchConfig(patchCfg)
	if err != nil {
		return "", err
	}

	log.Info(constant.ProviderSelfUpgradeReason, "found provider target image: %s", fullImage)
	return fullImage, nil
}

func (p *EnsureProviderSelfUpgrade) findProviderImageInPatchConfig(patchCfg *phaseutil.PatchConfig) (string, error) {
	for _, repo := range patchCfg.Repos {
		for _, subImage := range repo.SubImages {
			if image, found := p.findProviderImageInSubImage(subImage); found {
				return image, nil
			}
		}
	}
	return "", fmt.Errorf("provider image not found in patch config")
}

func (p *EnsureProviderSelfUpgrade) findProviderImageInSubImage(subImage phaseutil.SubImage) (string, bool) {
	for _, image := range subImage.Images {
		if p.isProviderImage(image) {
			if len(image.Tag) == 0 {
				continue
			}
			fullImage := fmt.Sprintf("%s/%s:%s",
				strings.TrimSuffix(subImage.SourceRepo, "/"),
				strings.TrimPrefix(image.Name, "/"),
				image.Tag[0])
			return fullImage, true
		}
	}
	return "", false
}

func (p *EnsureProviderSelfUpgrade) getPatchConfig(bkeCluster *bkev1beta1.BKECluster) (*phaseutil.PatchConfig, error) {
	ctx, c, _, _, log := p.Ctx.Untie()
	openFuyaoVersion := bkeCluster.Spec.ClusterConfig.Cluster.OpenFuyaoVersion
	log.Info(constant.ProviderSelfUpgradeReason, "openFuyaoVersion: %v", openFuyaoVersion)

	bkeCmKey := fmt.Sprintf("patch.%s", openFuyaoVersion)
	patchCmKey := fmt.Sprintf("cm.%s", openFuyaoVersion)

	localConfigMap := &corev1.ConfigMap{}
	if err := c.Get(ctx, constant.GetLocalConfigMapObjectKey(), localConfigMap); err != nil {
		log.Error(constant.ProviderSelfUpgradeFailed, "failed to get local cluster bke-config cm, err: %v", err)
		return nil, fmt.Errorf("get cm failed %v", err)
	}

	// Check if patch.<version> key exists
	if _, ok := localConfigMap.Data[bkeCmKey]; !ok {
		log.Info(constant.ProviderSelfUpgradeReason, "patch config %s does not exist (may be base version), skip", bkeCmKey)
		return nil, fmt.Errorf("patch info %s not found (non-patch version)", bkeCmKey)
	}

	// Read openfuyao-patch/cm.<version> ConfigMap
	cmKey := client.ObjectKey{
		Namespace: "openfuyao-patch",
		Name:      patchCmKey,
	}
	patchConfigMap := &corev1.ConfigMap{}
	if err := c.Get(ctx, cmKey, patchConfigMap); err != nil {
		log.Error(constant.ProviderSelfUpgradeFailed, "failed to get patch cm, err: %v", err)
		return nil, fmt.Errorf("get cm failed %v", err)
	}

	// Parse yaml config
	if _, ok := patchConfigMap.Data[openFuyaoVersion]; !ok {
		return nil, fmt.Errorf("patch info %s not found in patch config", openFuyaoVersion)
	}

	log.Info(constant.ProviderSelfUpgradeReason, "get patch config data length: %d", len(patchConfigMap.Data[openFuyaoVersion]))
	return phaseutil.GetPatchConfig(patchConfigMap.Data[openFuyaoVersion])
}

func (p *EnsureProviderSelfUpgrade) isProviderImage(image phaseutil.Image) bool {
	// Match by image name
	if strings.Contains(image.Name, providerImageName) {
		return true
	}

	// Match by PodInfo
	for _, podInfo := range image.UsedPodInfo {
		if podInfo.PodPrefix == providerDeploymentName && podInfo.NameSpace == providerNamespace {
			return true
		}
	}

	return false
}

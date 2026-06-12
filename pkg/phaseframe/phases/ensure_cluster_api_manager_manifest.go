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
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/common"
	bkeaddon "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/addon"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/condition"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

const (
	// EnsureClusterAPIManagerManifestName applies cluster-api 004-manage.yaml after postprocess.
	EnsureClusterAPIManagerManifestName confv1beta1.BKEClusterPhase = "EnsureClusterAPIManagerManifest"
)

type EnsureClusterAPIManagerManifest struct {
	phaseframe.BasePhase
}

func NewEnsureClusterAPIManagerManifest(ctx *phaseframe.PhaseContext) phaseframe.Phase {
	base := phaseframe.NewBasePhase(ctx, EnsureClusterAPIManagerManifestName)
	return &EnsureClusterAPIManagerManifest{BasePhase: base}
}

func (e *EnsureClusterAPIManagerManifest) NeedExecute(old *bkev1beta1.BKECluster, new *bkev1beta1.BKECluster) bool {
	if !e.BasePhase.DefaultNeedExecute(old, new) {
		return false
	}

	// Only run when cluster-api addon exists.
	version, ok := findAddonVersion(new, "cluster-api")
	if !ok || strings.TrimSpace(version) == "" {
		return false
	}

	// Skip if already applied.
	if v, ok := annotation.HasAnnotation(new, common.ClusterAPIManagerAppliedAnnotationKey); ok && v == "true" {
		return false
	}

	// Defer until postprocess is completed.
	if !condition.HasConditionStatus(bkev1beta1.NodesPostProcessCondition, new, confv1beta1.ConditionTrue) {
		e.SetStatus(bkev1beta1.PhaseWaiting)
		return true
	}

	e.SetStatus(bkev1beta1.PhaseWaiting)
	return true
}

func (e *EnsureClusterAPIManagerManifest) Execute() (ctrl.Result, error) {
	_, c, bkeCluster, _, log := e.Ctx.Untie()

	// Ensure postprocess completed (double check).
	bkeNodes, err := e.Ctx.NodeFetcher().GetBKENodesWrapperForCluster(e.Ctx, bkeCluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	if phaseutil.GetNeedPostProcessNodesWithBKENodes(bkeCluster, bkeNodes).Length() > 0 {
		condition.ConditionMark(bkeCluster, bkev1beta1.NodesPostProcessCondition, confv1beta1.ConditionFalse, constant.NodesPostProcessNotReadyReason, "")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, errors.Errorf("postprocess not finished")
	}

	version, ok := findAddonVersion(bkeCluster, "cluster-api")
	if !ok {
		return ctrl.Result{}, nil
	}
	addon, ok := findAddon(bkeCluster, "cluster-api")
	if !ok {
		return ctrl.Result{}, nil
	}

	targetClusterClient, err := kube.NewRemoteClientByBKECluster(e.Ctx.Context, e.Ctx.Client, bkeCluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	targetClusterClient.SetLogger(log.NormalLogger)
	targetClusterClient.SetBKELogger(log)

	managerYamlPath := filepath.Join(constant.K8sManifestsDir, "cluster-api", version, "004-manage.yaml")
	config := bkeinit.BkeConfig(*bkeCluster.Spec.ClusterConfig)
	repo := config.ImageRepo()

	nodes, err := e.Ctx.GetNodes()
	if err != nil {
		return ctrl.Result{}, err
	}

	param := map[string]interface{}{}
	if impl, ok := targetClusterClient.(*kube.Client); ok {
		param, err = impl.PrepareRenderParamForAddonFile(bkeCluster, addon, managerYamlPath, repo, nodes)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else {
		return ctrl.Result{}, errors.Errorf("unsupported remote client type %T", targetClusterClient)
	}

	task := kube.NewTask("cluster-api-manage", managerYamlPath, param).
		AddRepo(repo).
		SetOperate(bkeaddon.CreateAddon).
		SetWaiter(true, bkeinit.DefaultAddonTimeout, bkeinit.DefaultAddonInterval)

	log.Info(constant.AddonDeployingReason, "apply deferred cluster-api manage manifest, file=%s", managerYamlPath)
	if err := targetClusterClient.ApplyYaml(task); err != nil {
		log.Error(constant.AddonDeployFailedReason, "apply deferred cluster-api manage manifest failed: %s", err.Error())
		return ctrl.Result{}, err
	}

	annotation.SetAnnotation(bkeCluster, common.ClusterAPIManagerAppliedAnnotationKey, "true")
	log.Info(constant.AddonDeployedReason, "deferred cluster-api manage manifest applied for %q", utils.ClientObjNS(bkeCluster))
	if err := mergecluster.SyncStatusUntilComplete(c, bkeCluster); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func findAddonVersion(bkeCluster *bkev1beta1.BKECluster, addonName string) (string, bool) {
	if bkeCluster == nil || bkeCluster.Spec.ClusterConfig == nil {
		return "", false
	}
	for _, a := range bkeCluster.Spec.ClusterConfig.Addons {
		if a.Name == addonName {
			return a.Version, true
		}
	}
	return "", false
}

func findAddon(bkeCluster *bkev1beta1.BKECluster, addonName string) (*confv1beta1.Product, bool) {
	if bkeCluster == nil || bkeCluster.Spec.ClusterConfig == nil {
		return nil, false
	}
	for i := range bkeCluster.Spec.ClusterConfig.Addons {
		a := &bkeCluster.Spec.ClusterConfig.Addons[i]
		if a.Name == addonName {
			return a, true
		}
	}
	return nil, false
}

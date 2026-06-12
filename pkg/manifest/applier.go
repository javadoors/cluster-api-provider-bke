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

package manifest

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeaddon "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/addon"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

// ClusterApplier applies release bundle manifests to the BKECluster upgrade target API server.
type ClusterApplier struct {
	ctx        context.Context
	client     client.Client
	bkeCluster *bkev1beta1.BKECluster
	logger     *bkev1beta1.BKELogger
	nodes      bkenode.Nodes
}

// ClusterApplierConfig configures a ClusterApplier instance.
type ClusterApplierConfig struct {
	Context    context.Context
	Client     client.Client
	BKECluster *bkev1beta1.BKECluster
	Logger     *bkev1beta1.BKELogger
	Nodes      bkenode.Nodes
}

// NewClusterApplier creates an applier that uses pkg/kube ApplyYaml.
func NewClusterApplier(cfg ClusterApplierConfig) *ClusterApplier {
	return &ClusterApplier{
		ctx:        cfg.Context,
		client:     cfg.Client,
		bkeCluster: cfg.BKECluster,
		logger:     cfg.Logger,
		nodes:      cfg.Nodes,
	}
}

// ApplyComponent renders and applies all manifests in the package.
func (a *ClusterApplier) ApplyComponent(ctx context.Context, pkg *ComponentPackage) error {
	if pkg == nil {
		return fmt.Errorf("component package is nil")
	}
	if len(pkg.Manifests) == 0 {
		return nil
	}
	if a == nil || a.client == nil || a.bkeCluster == nil {
		return fmt.Errorf("cluster manifest applier is not configured")
	}

	kubeClient, err := a.kubeClient()
	if err != nil {
		return err
	}
	params, err := a.renderParams(kubeClient)
	if err != nil {
		return errors.Wrapf(err, "build render params for %s", pkg.Name)
	}
	if err := a.guardComponentInstalled(ctx, kubeClient, pkg, params); err != nil {
		return err
	}
	a.prepareKubeClientForApply(kubeClient, ctx)
	return a.applyPackageManifests(kubeClient, pkg, params)
}

func (a *ClusterApplier) guardComponentInstalled(
	ctx context.Context,
	kubeClient kube.RemoteKubeClient,
	pkg *ComponentPackage,
	params map[string]interface{},
) error {
	clientset, _ := kubeClient.KubeClient()
	if clientset == nil {
		return nil
	}
	installed, err := IsComponentInstalled(ctx, clientset, pkg.Name, pkg.Manifests, params)
	if err != nil {
		return errors.Wrapf(err, "probe install state for %s", pkg.Name)
	}
	if installed {
		return nil
	}
	if a.logger != nil {
		a.logger.Info("skip manifest component upgrade", "component", pkg.Name, "reason", SkipReasonNotInstalled)
	}
	return NewSkipNotInstalledError(pkg.Name)
}

func (a *ClusterApplier) prepareKubeClientForApply(kubeClient kube.RemoteKubeClient, ctx context.Context) {
	impl, ok := kubeClient.(*kube.Client)
	if !ok {
		return
	}
	if a.logger != nil {
		impl.SetBKELogger(a.logger)
		if a.logger.NormalLogger != nil {
			impl.SetLogger(a.logger.NormalLogger)
		}
	}
	applyCtx := a.ctx
	if applyCtx == nil {
		applyCtx = ctx
	}
	if applyCtx != nil {
		impl.Ctx = applyCtx
	}
}

func (a *ClusterApplier) imageRepo() string {
	if a == nil || a.bkeCluster == nil || a.bkeCluster.Spec.ClusterConfig == nil {
		return ""
	}
	cfg := bkeinit.BkeConfig(*a.bkeCluster.Spec.ClusterConfig)
	return cfg.ImageRepo()
}

func (a *ClusterApplier) applyPackageManifests(
	kubeClient kube.RemoteKubeClient,
	pkg *ComponentPackage,
	params map[string]interface{},
) error {
	repo := a.imageRepo()
	for i, doc := range pkg.Manifests {
		if len(doc) == 0 {
			continue
		}
		task := kube.NewTask(fmt.Sprintf("%s-%d", pkg.Name, i), "", params).
			AddRepo(repo).
			SetOperate(bkeaddon.UpgradeAddon).
			SetWaiter(true, bkeinit.DefaultAddonTimeout, bkeinit.DefaultAddonInterval)
		task.ManifestContent = doc
		if err := kubeClient.ApplyYaml(task); err != nil {
			return errors.Wrapf(err, "apply manifest %d for component %s", i, pkg.Name)
		}
	}
	return nil
}

func (a *ClusterApplier) kubeClient() (kube.RemoteKubeClient, error) {
	targetClusterClient, err := kube.NewRemoteClientByBKECluster(a.ctx, a.client, a.bkeCluster)
	if err != nil {
		if a.logger != nil {
			a.logger.Error(constant.InternalErrorReason, "failed to get BKECluster %q remote cluster client", utils.ClientObjNS(a.bkeCluster))
		}
		return nil, err
	}
	clientset, _ := targetClusterClient.KubeClient()
	if clientset == nil {
		return nil, fmt.Errorf("failed to get remote client for BKECluster %q", utils.ClientObjNS(a.bkeCluster))
	}
	return targetClusterClient, nil
}

func (a *ClusterApplier) renderParams(kubeClient kube.RemoteKubeClient) (map[string]interface{}, error) {
	impl, ok := kubeClient.(*kube.Client)
	if !ok {
		return fallbackRenderParams(a.bkeCluster), nil
	}
	params, err := impl.RenderParamsForCluster(a.bkeCluster, a.nodes)
	if err != nil {
		return nil, err
	}
	if params == nil {
		return fallbackRenderParams(a.bkeCluster), nil
	}
	return params, nil
}

func fallbackRenderParams(cluster *bkev1beta1.BKECluster) map[string]interface{} {
	params := map[string]interface{}{
		"namespace": cluster.Namespace,
	}
	if cluster.Spec.ClusterConfig != nil {
		cfg := bkeinit.BkeConfig(*cluster.Spec.ClusterConfig)
		params["repo"] = cfg.ImageRepo()
		spec := cluster.Spec.ClusterConfig.Cluster
		params["kubernetesVersion"] = spec.KubernetesVersion
		params["openFuyaoVersion"] = spec.OpenFuyaoVersion
	}
	return params
}

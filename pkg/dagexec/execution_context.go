/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package dagexec

import (
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/manifest"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/upgrade"
)

// ExecutionContext is the phaseframe-free context for ExecuteDAG / ComponentExecutor.
// Controllers assemble it via buildExecutionContext; NewExecutionContext fills
// TemplateContext from Cluster and tolerates nil ClusterConfig.
type ExecutionContext struct {
	OldCluster             *bkev1beta1.BKECluster
	Cluster                *bkev1beta1.BKECluster
	ComponentStatusUpdater ComponentStatusUpdater
	Log                    *bkev1beta1.BKELogger
	VersionContext         *upgrade.VersionContext
	TemplateContext        manifest.TemplateContext
	TargetClient           kubernetes.Interface
	// Client is the management-cluster client used for DeclarativeUpgrade status patches.
	Client client.Client
}

// NewExecutionContextOptions carries optional dependencies for NewExecutionContext.
type NewExecutionContextOptions struct {
	OldCluster             *bkev1beta1.BKECluster
	Cluster                *bkev1beta1.BKECluster
	Log                    *bkev1beta1.BKELogger
	VersionContext         *upgrade.VersionContext
	TargetClient           kubernetes.Interface
	ComponentStatusUpdater ComponentStatusUpdater
	Client                 client.Client
}

// NewExecutionContext builds an ExecutionContext and fills TemplateContext from Cluster.
// A nil Cluster or nil ClusterConfig must not panic; template version fields stay empty.
func NewExecutionContext(opts NewExecutionContextOptions) *ExecutionContext {
	execCtx := &ExecutionContext{
		OldCluster:             opts.OldCluster,
		Cluster:                opts.Cluster,
		ComponentStatusUpdater: opts.ComponentStatusUpdater,
		Log:                    opts.Log,
		VersionContext:         opts.VersionContext,
		TargetClient:           opts.TargetClient,
		Client:                 opts.Client,
	}
	execCtx.TemplateContext = buildTemplateContext(opts.Cluster)
	return execCtx
}

func buildTemplateContext(cluster *bkev1beta1.BKECluster) manifest.TemplateContext {
	var tmpl manifest.TemplateContext
	if cluster == nil {
		return tmpl
	}
	tmpl.ClusterName = cluster.GetName()
	tmpl.Namespace = cluster.GetNamespace()
	if cluster.Spec.ClusterConfig == nil {
		return tmpl
	}
	spec := cluster.Spec.ClusterConfig.Cluster
	tmpl.KubernetesVersion = spec.KubernetesVersion
	tmpl.OpenFuyaoVersion = spec.OpenFuyaoVersion
	return tmpl
}

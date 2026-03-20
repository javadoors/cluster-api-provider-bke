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
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
)

// phaseFlow register all phases
var (
	// CommonPhases common phases
	CommonPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
		NewEnsureFinalizer,
		NewEnsurePaused,
		NewEnsureClusterManage,
		NewEnsureDeleteOrReset,
		NewEnsureDryRun,
	}

	// DeployPhases deploy phases
	DeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
		NewEnsureBKEAgent,
		NewEnsureNodesEnv,
		NewEnsureClusterAPIObj,
		NewEnsureCerts,
		NewEnsureLoadBalance,
		NewEnsureMasterInit,
		NewEnsureMasterJoin,
		NewEnsureWorkerJoin,
		NewEnsureAddonDeploy,
		NewEnsureNodesPostProcess,
		NewEnsureAgentSwitch,
	}

	// PostDeployPhases post deploy phases
	PostDeployPhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
		NewEnsureProviderSelfUpgrade,
		NewEnsureAgentUpgrade,
		NewEnsureContainerdUpgrade,
		NewEnsureEtcdUpgrade,
		NewEnsureWorkerUpgrade,
		NewEnsureMasterUpgrade,
		NewEnsureWorkerDelete,
		NewEnsureMasterDelete,
		NewEnsureComponentUpgrade,
		NewEnsureCluster,
	}

	// DeletePhases delete phases
	DeletePhases = []func(ctx *phaseframe.PhaseContext) phaseframe.Phase{
		NewEnsurePaused,
		NewEnsureDeleteOrReset,
	}
)

var (
	ClusterInitPhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureFinalizerName,
		EnsureCertsName,
		EnsureClusterAPIObjName,
		EnsureMasterInitName,
		EnsureBKEAgentName,
		EnsureNodesEnvName,
		EnsureLoadBalanceName,
		EnsureAgentSwitchName,
	}
	ClusterUpgradePhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureAgentUpgradeName,
		EnsureContainerdUpgradeName,
		EnsureMasterUpgradeName,
		EnsureWorkerUpgradeName,
		EnsureComponentUpgradeName,
	}
	ClusterScaleMasterDownPhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureMasterDeleteName,
	}
	ClusterScaleMasterUpPhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureMasterJoinName,
	}
	ClusterScaleWorkerDownPhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureWorkerDeleteName,
	}

	ClusterScaleWorkerUpPhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureWorkerJoinName,
	}
	ClusterAddonsPhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureAddonDeployName,
	}
	ClusterDeletePhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureDeleteOrResetName,
	}
	ClusterDryRunPhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureDryRunName,
	}
	ClusterPausedPhaseNames = []confv1beta1.BKEClusterPhase{
		EnsurePausedName,
	}
	ClusterManagePhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureClusterManageName,
	}
	CustomSetStatusPhaseNames = []confv1beta1.BKEClusterPhase{
		EnsureClusterName,
	}
	ClusterDeleteResetPhaseNames = []confv1beta1.BKEClusterPhase{
		EnsurePausedName,
		EnsureDeleteOrResetName,
	}
)

var PhaseNameCNMap = map[confv1beta1.BKEClusterPhase]string{
	EnsureFinalizerName:           "部署任务创建",
	EnsurePausedName:              "集群管理暂停",
	EnsureDeleteOrResetName:       "集群删除",
	EnsureClusterManageName:       "纳管现有集群",
	EnsureDryRunName:              "DryRun部署",
	EnsureBKEAgentName:            "推送Agent",
	EnsureNodesEnvName:            "节点环境准备",
	EnsureClusterAPIObjName:       "ClusterAPI对接",
	EnsureCertsName:               "集群证书创建",
	EnsureLoadBalanceName:         "集群入口配置",
	EnsureMasterInitName:          "Master初始化",
	EnsureMasterJoinName:          "Master加入",
	EnsureWorkerJoinName:          "Worker加入",
	EnsureAgentSwitchName:         "Agent监听切换",
	EnsureAddonDeployName:         "集群组件部署",
	EnsureNodesPostProcessName:    "后置脚本处理",
	EnsureProviderSelfUpgradeName: "provider自升级",
	EnsureAgentUpgradeName:        "Agent升级",
	EnsureContainerdUpgradeName:   "Containerd升级",
	EnsureWorkerUpgradeName:       "Worker升级",
	EnsureMasterUpgradeName:       "Master升级",
	EnsureComponentUpgradeName:    "openFuyao核心组件升级",
	EnsureWorkerDeleteName:        "Worker删除",
	EnsureMasterDeleteName:        "Master删除",
	EnsureClusterName:             "集群健康检查",
}

func ConvertPhaseNameToCN(phase string) string {
	if v, ok := PhaseNameCNMap[confv1beta1.BKEClusterPhase(phase)]; ok {
		return v
	}
	return phase
}

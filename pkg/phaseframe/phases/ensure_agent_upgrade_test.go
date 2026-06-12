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
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	ctrl "sigs.k8s.io/controller-runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil/agentssh"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/upgrade"
)

func TestEnsureAgentUpgradeConstants(t *testing.T) {
	assert.Equal(t, "EnsureAgentUpgrade", string(EnsureAgentUpgradeName))
	assert.Equal(t, "bkeagent", bkeagentAddonName)
}

func TestNewEnsureAgentUpgrade(t *testing.T) {
	InitinitPhaseContextFun()
	phase := NewEnsureAgentUpgrade(initPhaseContext)
	assert.NotNil(t, phase)
	assert.IsType(t, &EnsureAgentUpgrade{}, phase)
}

func TestEnsureAgentUpgrade_Version(t *testing.T) {
	InitinitPhaseContextFun()
	e := NewEnsureAgentUpgrade(initPhaseContext).(*EnsureAgentUpgrade)

	initPhaseContext.BKECluster = &bkev1beta1.BKECluster{
		Status: confv1beta1.BKEClusterStatus{
			AddonStatus: []confv1beta1.Product{{Name: bkeagentAddonName, Version: "v1.2.3"}},
		},
	}
	assert.Equal(t, "v1.2.3", e.Version())

	initPhaseContext.BKECluster = &bkev1beta1.BKECluster{}
	assert.Equal(t, "", e.Version())
}

func TestEnsureAgentUpgrade_NeedExecute(t *testing.T) {
	InitinitPhaseContextFun()
	e := NewEnsureAgentUpgrade(initPhaseContext).(*EnsureAgentUpgrade)

	vc := upgrade.NewVersionContext()
	vc.SetCurrent(upgrade.ComponentBKEAgent, "v1.0.0")
	vc.SetTarget(upgrade.ComponentBKEAgent, "v1.0.1")
	initPhaseContext.SetVersionContext(vc)

	oldCluster := &bkev1beta1.BKECluster{}
	newCluster := oldCluster.DeepCopy()
	assert.True(t, e.NeedExecute(oldCluster, newCluster))

	vc.SetCurrent(upgrade.ComponentBKEAgent, "v1.0.1")
	assert.False(t, e.NeedExecute(oldCluster, newCluster))
}

func TestBKEAgentArtifactName(t *testing.T) {
	cfg := bkeinit.BkeConfig{}
	assert.Equal(t, agentssh.DefaultBKEAgentArtifact, agentssh.BinaryArtifactName(cfg, ""))
	assert.Equal(t, "bkeagent-2.1.0-linux-{.arch}", agentssh.BinaryArtifactName(cfg, "v2.1.0"))

	cfg.CustomExtra = map[string]string{"bkeagent": "custom-bkeagent-{.arch}"}
	assert.Equal(t, "custom-bkeagent-{.arch}", agentssh.BinaryArtifactName(cfg, "v9.9.9"))
}

func TestEnsureAgentUpgrade_AgentTargetVersion(t *testing.T) {
	InitinitPhaseContextFun()
	e := NewEnsureAgentUpgrade(initPhaseContext).(*EnsureAgentUpgrade)

	vc := upgrade.NewVersionContext()
	vc.SetTarget(upgrade.ComponentBKEAgent, "v2.1.0")
	initPhaseContext.SetVersionContext(vc)
	assert.Equal(t, "v2.1.0", e.agentTargetVersion())

	vc = upgrade.NewVersionContext()
	vc.SetTarget(legacyReleaseBKEAgentComponent, "v2.2.0")
	initPhaseContext.SetVersionContext(vc)
	assert.Equal(t, "v2.2.0", e.agentTargetVersion())
}

func TestAgentSSHParamsFromCluster(t *testing.T) {
	InitinitPhaseContextFun()
	cluster := initNewBkeCluster.DeepCopy()
	cluster.Spec.ClusterConfig.Cluster.HTTPRepo = confv1beta1.Repo{
		Domain: "repo.example.com", Port: "8080", Prefix: "files",
	}

	params := agentssh.ParamsFromCluster(cluster, "")
	assert.Equal(t, "http://repo.example.com:8080/files", params.BaseURL)
	assert.Equal(t, agentssh.DefaultBKEAgentArtifact, params.BinaryArtifact)

	params = agentssh.ParamsFromCluster(cluster, "v2.1.0")
	assert.Equal(t, "bkeagent-2.1.0-linux-{.arch}", params.BinaryArtifact)
	assert.Equal(t, "http://repo.example.com:8080/files/bkeagent-2.1.0-linux-amd64",
		agentssh.BinaryURLForArch(params, "amd64"))

	cluster.Spec.ClusterConfig.CustomExtra = map[string]string{"bkeagent": "custom-bkeagent-{.arch}"}
	params = agentssh.ParamsFromCluster(cluster, "v9.9.9")
	assert.Equal(t, "custom-bkeagent-{.arch}", params.BinaryArtifact)
}

func TestEnsureAgentUpgrade_Execute_SSHUpgrade(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	InitinitPhaseContextFun()
	e := NewEnsureAgentUpgrade(initPhaseContext).(*EnsureAgentUpgrade)
	patches.ApplyPrivateMethod(e, "upgradeBKEAgentViaSSH", func(_ *EnsureAgentUpgrade) error {
		return nil
	})

	result, err := e.Execute()
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

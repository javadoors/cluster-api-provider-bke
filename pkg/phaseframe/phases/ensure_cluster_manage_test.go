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
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
)

func TestEnsureClusterManageConstants(t *testing.T) {
	assert.Equal(t, "EnsureClusterManage", string(EnsureClusterManageName))
}

func TestNewEnsureClusterManage(t *testing.T) {
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		Client:     &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{},
		Log:        createTestLogger(),
		Scheme:     runtime.NewScheme(),
	}
	phase := NewEnsureClusterManage(ctx)
	assert.NotNil(t, phase)
	assert.IsType(t, &EnsureClusterManage{}, phase)
}

func TestEnsureClusterManage_NeedExecute(t *testing.T) {
	t.Skip("Requires complex mocking of DefaultNeedExecute")
}

func TestEnsureClusterManage_Execute_Error(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		Client:     &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{},
		Log:        createTestLogger(),
	}
	e := NewEnsureClusterManage(ctx).(*EnsureClusterManage)
	patches.ApplyPrivateMethod(e, "collectBaseInfo", func(_ *EnsureClusterManage) error {
		return assert.AnError
	})
	result, err := e.Execute()
	assert.Error(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestEnsureClusterManage_CheckAgentNeedPush(t *testing.T) {
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		Client:     &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{},
		Log:        createTestLogger(),
	}
	e := NewEnsureClusterManage(ctx).(*EnsureClusterManage)
	result := e.checkAgentNeedPush(bkenode.Nodes{})
	assert.True(t, result)
}

func TestEnsureClusterManage_DistributeMasterNodesCerts(t *testing.T) {
	t.Skip("Requires complex setup with remote client")
}

func TestEnsureClusterManage_DistributeWorkerNodesCerts(t *testing.T) {
	t.Skip("Requires complex setup with remote client")
}

func TestEnsureClusterManage_BackupBocloudClusterData(t *testing.T) {
	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		Client:     &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{},
		Log:        createTestLogger(),
	}
	e := NewEnsureClusterManage(ctx).(*EnsureClusterManage)
	err := e.backupBocloudClusterData(bkenode.Nodes{})
	assert.Error(t, err)
}

func TestCreateCertCommandSpec(t *testing.T) {
	params := CreateCertCommandSpecParams{
		CertPluginName:  "test",
		ClusterName:     "test",
		Namespace:       "default",
		CertificatesDir: "/etc/kubernetes/pki",
	}
	spec := createCertCommandSpec(params)
	assert.NotNil(t, spec)
}

func TestNewBaseCommandParams(t *testing.T) {
	params := newBaseCommandParams(context.Background(), &fakeClient{}, &bkev1beta1.BKECluster{}, runtime.NewScheme())
	assert.NotNil(t, params.Ctx)
	assert.NotNil(t, params.Client)
	assert.NotNil(t, params.OwnerObj)
	assert.NotNil(t, params.Scheme)
}

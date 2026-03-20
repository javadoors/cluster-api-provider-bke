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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
)

func createTestEnsureMasterInit() *EnsureMasterInit {
	logger := createTestLogger()
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: confv1beta1.BKEClusterSpec{},
	}

	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		Client:     &fakeClient{},
		BKECluster: bkeCluster,
		Scheme:     scheme,
		Log:        logger,
	}

	return &EnsureMasterInit{
		BasePhase: phaseframe.NewBasePhase(ctx, EnsureMasterInitName),
	}
}


func TestEnsureMasterInitConstants(t *testing.T) {
	assert.Equal(t, "EnsureMasterInit", string(EnsureMasterInitName))
	assert.Equal(t, 10, MasterInitLogIntervalCount)
	assert.Equal(t, 2, MasterInitSleepSeconds)
	assert.Equal(t, 1, MasterInitPollIntervalSeconds)
}

func TestNewEnsureMasterInit(t *testing.T) {
	logger := createTestLogger()
	ctx := &phaseframe.PhaseContext{
		Context: context.Background(),
		Client:  &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		},
		Scheme: runtime.NewScheme(),
		Log:    logger,
	}

	phase := NewEnsureMasterInit(ctx)
	assert.NotNil(t, phase)
	assert.IsType(t, &EnsureMasterInit{}, phase)
}

func TestEnsureMasterInit_ValidateMasterNodes_NoMasterNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureMasterInit()

	patches.ApplyFunc((*nodeutil.NodeFetcher).GetNodesForBKECluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster) (bkenode.Nodes, error) {
		return bkenode.Nodes{}, nil
	})

	params := ValidateMasterNodesParams{
		Ctx: e.Ctx,
	}

	nodes, count, err := e.validateMasterNodes(params)
	assert.Error(t, err)
	assert.Nil(t, nodes)
	assert.Equal(t, 0, count)
}

func TestEnsureMasterInit_NeedExecute_NotReady(t *testing.T) {
	e := createTestEnsureMasterInit()
	now := metav1.Now()
	old := &bkev1beta1.BKECluster{}
	new := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			DeletionTimestamp: &now,
		},
	}

	result := e.NeedExecute(old, new)
	assert.False(t, result)
}


func TestValidateMasterNodesParams_Structure(t *testing.T) {
	e := createTestEnsureMasterInit()
	params := ValidateMasterNodesParams{
		Ctx: e.Ctx,
	}
	assert.NotNil(t, params.Ctx)
}

func TestSetupConditionAndRefreshParams_Structure(t *testing.T) {
	e := createTestEnsureMasterInit()
	params := SetupConditionAndRefreshParams{
		Ctx: e.Ctx,
	}
	assert.NotNil(t, params.Ctx)
}


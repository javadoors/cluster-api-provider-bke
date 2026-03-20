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

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
)

func createTestEnsureMasterJoin() *EnsureMasterJoin {
	logger := createTestLogger()
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{},
		},
	}

	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	ctx := &phaseframe.PhaseContext{
		Context:    context.Background(),
		Client:     &fakeClient{},
		BKECluster: bkeCluster,
		Scheme:     scheme,
		Log:        logger,
	}

	return &EnsureMasterJoin{
		BasePhase:   phaseframe.NewBasePhase(ctx, EnsureMasterJoinName),
		nodesToJoin: bkenode.Nodes{},
	}
}


func TestEnsureMasterJoinConstants(t *testing.T) {
	assert.Equal(t, "EnsureMasterJoin", string(EnsureMasterJoinName))
	assert.Equal(t, 10, LogOutputInterval)
}

func TestNewEnsureMasterJoin(t *testing.T) {
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

	phase := NewEnsureMasterJoin(ctx)
	assert.NotNil(t, phase)
	assert.IsType(t, &EnsureMasterJoin{}, phase)
}

func TestEnsureMasterJoin_NeedExecute_DefaultNeedExecuteFalse(t *testing.T) {
	e := createTestEnsureMasterJoin()
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

func TestMasterJoinParams_Structure(t *testing.T) {
	params := MasterJoinParams{
		Ctx:        context.Background(),
		Client:     &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{},
		Log:        createTestLogger(),
	}
	assert.NotNil(t, params.Ctx)
	assert.NotNil(t, params.Client)
	assert.NotNil(t, params.BKECluster)
	assert.NotNil(t, params.Log)
}



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
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/command"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/nodeutil"
)

func createTestEnsureLoadBalance() *EnsureLoadBalance {
	logger := createTestLogger()
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: confv1beta1.BKEClusterSpec{
			ControlPlaneEndpoint: confv1beta1.APIEndpoint{
				Host: "192.168.1.100",
				Port: 6443,
			},
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{
					Kubelet: &confv1beta1.Kubelet{
						ManifestsDir: "/etc/kubernetes/manifests",
					},
				},
				CustomExtra: map[string]string{
					"masterVirtualRouterId": "50",
				},
			},
		},
		Status: confv1beta1.BKEClusterStatus{
			Ready: false,
		},
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

	return &EnsureLoadBalance{
		BasePhase: phaseframe.NewBasePhase(ctx, EnsureLoadBalanceName),
	}
}

func TestEnsureLoadBalanceConstants(t *testing.T) {
	assert.Equal(t, "EnsureLoadBalance", string(EnsureLoadBalanceName))
}

func TestNewEnsureLoadBalance(t *testing.T) {
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

	phase := NewEnsureLoadBalance(ctx)
	assert.NotNil(t, phase)
	assert.IsType(t, &EnsureLoadBalance{}, phase)
}

func TestEnsureLoadBalance_NeedExecute_NotReady(t *testing.T) {
	e := createTestEnsureLoadBalance()
	e.Ctx.BKECluster.Status.Ready = false

	old := &bkev1beta1.BKECluster{}
	new := e.Ctx.BKECluster

	result := e.NeedExecute(old, new)
	assert.True(t, result)
}

func TestEnsureLoadBalance_NeedExecute_HostIsNode(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureLoadBalance()
	e.Ctx.BKECluster.Status.Ready = true
	e.Ctx.BKECluster.Spec.ControlPlaneEndpoint.Host = "192.168.1.1"

	patches.ApplyFunc((*nodeutil.NodeFetcher).GetNodesForBKECluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster) (bkenode.Nodes, error) {
		return bkenode.Nodes{{IP: "192.168.1.1"}}, nil
	})

	old := &bkev1beta1.BKECluster{}
	new := e.Ctx.BKECluster

	result := e.NeedExecute(old, new)
	assert.False(t, result)
}

func TestEnsureLoadBalance_NeedExecute_NoLoadBalanceNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureLoadBalance()
	e.Ctx.BKECluster.Status.Ready = true

	patches.ApplyFunc((*nodeutil.NodeFetcher).GetNodesForBKECluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster) (bkenode.Nodes, error) {
		return bkenode.Nodes{{IP: "192.168.1.1"}}, nil
	})

	patches.ApplyFunc((*nodeutil.NodeFetcher).GetBKENodesWrapperForCluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster) (bkev1beta1.BKENodes, error) {
		return bkev1beta1.BKENodes{}, nil
	})

	patches.ApplyFunc(phaseutil.GetNeedLoadBalanceNodesWithBKENodes, func(ctx context.Context, c client.Client, cluster *bkev1beta1.BKECluster, nodes bkev1beta1.BKENodes) bkenode.Nodes {
		return bkenode.Nodes{}
	})

	old := &bkev1beta1.BKECluster{}
	new := e.Ctx.BKECluster

	result := e.NeedExecute(old, new)
	assert.False(t, result)
}

func TestEnsureLoadBalance_NeedExecute_WithLoadBalance(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureLoadBalance()
	e.Ctx.BKECluster.Status.Ready = true

	patches.ApplyFunc((*nodeutil.NodeFetcher).GetNodesForBKECluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster) (bkenode.Nodes, error) {
		return bkenode.Nodes{{IP: "192.168.1.1", Role: []string{"master"}}}, nil
	})

	patches.ApplyFunc((*nodeutil.NodeFetcher).GetBKENodesWrapperForCluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster) (bkev1beta1.BKENodes, error) {
		return bkev1beta1.BKENodes{{Spec: confv1beta1.BKENodeSpec{IP: "192.168.1.1"}}}, nil
	})

	patches.ApplyFunc(phaseutil.GetNeedLoadBalanceNodesWithBKENodes, func(ctx context.Context, c client.Client, cluster *bkev1beta1.BKECluster, nodes bkev1beta1.BKENodes) bkenode.Nodes {
		return bkenode.Nodes{{IP: "192.168.1.1"}}
	})

	old := &bkev1beta1.BKECluster{}
	new := e.Ctx.BKECluster

	result := e.NeedExecute(old, new)
	assert.True(t, result)
}

func TestEnsureLoadBalance_ConfiguringLoadBalancer_NoMasterNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureLoadBalance()

	patches.ApplyFunc((*nodeutil.NodeFetcher).GetNodesForBKECluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster) (bkenode.Nodes, error) {
		return bkenode.Nodes{}, nil
	})

	err := e.ConfiguringLoadBalancer()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no master nodes found")
}

func TestEnsureLoadBalance_ConfiguringLoadBalancer_NodeNotReady(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureLoadBalance()

	patches.ApplyFunc((*nodeutil.NodeFetcher).GetNodesForBKECluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster) (bkenode.Nodes, error) {
		return bkenode.Nodes{{IP: "192.168.1.1", Role: []string{"master"}}}, nil
	})

	patches.ApplyFunc((*nodeutil.NodeFetcher).GetNodeStateFlagForCluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, flag int) (bool, error) {
		return false, nil
	})

	err := e.ConfiguringLoadBalancer()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent is not ready")
}


func TestEnsureLoadBalance_CreateLoadBalancerCommand_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureLoadBalance()
	nodes := bkenode.Nodes{{IP: "192.168.1.1", Role: []string{"master"}}}

	patches.ApplyMethod(&command.HA{}, "New", func(_ *command.HA) error {
		return nil
	})

	cmd, err := e.createLoadBalancerCommand(nodes)
	assert.NoError(t, err)
	assert.NotNil(t, cmd)
}

func TestEnsureLoadBalance_CreateLoadBalancerCommand_NewError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureLoadBalance()
	nodes := bkenode.Nodes{{IP: "192.168.1.1", Role: []string{"master"}}}

	patches.ApplyMethod(&command.HA{}, "New", func(_ *command.HA) error {
		return assert.AnError
	})

	cmd, err := e.createLoadBalancerCommand(nodes)
	assert.NoError(t, err)
	assert.Nil(t, cmd)
}

func TestEnsureLoadBalance_ExecuteAndHandleLoadBalancer_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureLoadBalance()
	haCmd := &command.HA{}

	patches.ApplyMethod(haCmd, "Wait", func(_ *command.HA) (error, []string, []string) {
		return nil, []string{"192.168.1.1"}, []string{}
	})

	patches.ApplyFunc((*nodeutil.NodeFetcher).SetNodeStateWithMessageForCluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, state confv1beta1.NodeState, message string) error {
		return nil
	})

	patches.ApplyFunc((*nodeutil.NodeFetcher).MarkNodeStateFlagForCluster, func(_ *nodeutil.NodeFetcher, ctx context.Context, cluster *bkev1beta1.BKECluster, nodeIP string, flag int) error {
		return nil
	})

	patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(c client.Client, bkeCluster *bkev1beta1.BKECluster, patchs ...mergecluster.PatchFunc) error {
		return nil
	})

	err := e.executeAndHandleLoadBalancer(haCmd)
	assert.NoError(t, err)
	assert.True(t, e.Ctx.BKECluster.Status.Ready)
}


func TestEnsureLoadBalance_Execute_Success(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureLoadBalance()

	patches.ApplyMethod(e, "ConfiguringLoadBalancer", func(_ *EnsureLoadBalance) error {
		return nil
	})

	result, err := e.Execute()
	assert.NoError(t, err)
	assert.False(t, result.Requeue)
}

func TestEnsureLoadBalance_Execute_Error(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	e := createTestEnsureLoadBalance()

	patches.ApplyMethod(e, "ConfiguringLoadBalancer", func(_ *EnsureLoadBalance) error {
		return assert.AnError
	})

	result, err := e.Execute()
	assert.Error(t, err)
	assert.False(t, result.Requeue)
}



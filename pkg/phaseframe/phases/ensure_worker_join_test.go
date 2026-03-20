/*
 *
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 *
 */
package phases

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe"
)

func TestEnsureWorkerJoinConstants(t *testing.T) {
	assert.Equal(t, "EnsureWorkerJoin", string(EnsureWorkerJoinName))
}

func TestNewEnsureWorkerJoin(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	_ = clusterv1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx)
	assert.NotNil(t, phase)

	workerJoin, ok := phase.(*EnsureWorkerJoin)
	assert.True(t, ok)
	assert.NotNil(t, workerJoin)
}

func TestEnsureWorkerJoin_CategorizeJoinedNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	phase.nodesToJoin = bkenode.Nodes{
		{IP: "192.168.1.1", Hostname: "node1"},
		{IP: "192.168.1.2", Hostname: "node2"},
		{IP: "192.168.1.3", Hostname: "node3"},
	}

	successMap := &sync.Map{}
	successMap.Store(0, phase.nodesToJoin[0])
	successMap.Store(2, phase.nodesToJoin[2])

	successNodes, failedNodes := phase.categorizeJoinedNodes(successMap)
	assert.Equal(t, 2, len(successNodes))
	assert.Equal(t, 1, len(failedNodes))
	assert.Equal(t, "192.168.1.2", failedNodes[0].IP)
}

func TestEnsureWorkerJoin_IsAllNodesProcessed(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	phase.nodesToJoin = bkenode.Nodes{
		{IP: "192.168.1.1", Hostname: "node1"},
		{IP: "192.168.1.2", Hostname: "node2"},
	}

	successMap := &sync.Map{}
	failedMap := &sync.Map{}

	// Not all processed
	done, success, failed := phase.isAllNodesProcessed(successMap, failedMap)
	assert.False(t, done)
	assert.Equal(t, 0, success)
	assert.Equal(t, 0, failed)

	// All processed
	successMap.Store(0, phase.nodesToJoin[0])
	failedMap.Store(1, phase.nodesToJoin[1])

	done, success, failed = phase.isAllNodesProcessed(successMap, failedMap)
	assert.True(t, done)
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, failed)
}

func TestEnsureWorkerJoin_LogProgressIfNeeded(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	phase.nodesToJoin = bkenode.Nodes{
		{IP: "192.168.1.1", Hostname: "node1"},
	}

	successMap := &sync.Map{}
	failedMap := &sync.Map{}

	// Should not log (pollCount not multiple of 10)
	phase.logProgressIfNeeded(5, successMap, failedMap, ctx.Log)

	// Should log (pollCount is multiple of 10)
	phase.logProgressIfNeeded(10, successMap, failedMap, ctx.Log)
}

func TestEnsureWorkerJoin_LogFailedNodesSummary(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	phase.nodesToJoin = bkenode.Nodes{
		{IP: "192.168.1.1", Hostname: "node1"},
		{IP: "192.168.1.2", Hostname: "node2"},
	}

	successNodes := bkenode.Nodes{{IP: "192.168.1.1", Hostname: "node1"}}
	failedNodes := bkenode.Nodes{{IP: "192.168.1.2", Hostname: "node2"}}

	phase.logFailedNodesSummary(ctx.Log, successNodes, failedNodes)
}

func TestEnsureWorkerJoin_LogFailedNodesGuidance(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	phase.logFailedNodesGuidance(ctx.Log)
}

func TestEnsureWorkerJoin_LogSuccessResult(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	phase.nodesToJoin = bkenode.Nodes{
		{IP: "192.168.1.1", Hostname: "node1"},
		{IP: "192.168.1.2", Hostname: "node2"},
	}

	successNodes := bkenode.Nodes{{IP: "192.168.1.1", Hostname: "node1"}}
	failedNodes := bkenode.Nodes{{IP: "192.168.1.2", Hostname: "node2"}}

	phase.logSuccessResult(ctx.Log, successNodes, failedNodes)
}

func TestEnsureWorkerJoin_LogTimeoutResult(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	failedNodes := bkenode.Nodes{{IP: "192.168.1.1", Hostname: "node1"}}

	phase.logTimeoutResult(ctx.Log, failedNodes)
}

func TestEnsureWorkerJoin_HandleBocloudClusterConfig_NotBocloudCluster(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)

	params := HandleBocloudClusterConfigParams{
		Ctx:        context.Background(),
		Client:     c,
		BKECluster: bkeCluster,
		Log:        ctx.Log,
	}

	err := phase.handleBocloudClusterConfig(params)
	assert.NoError(t, err)
}

func TestEnsureWorkerJoin_DetermineDeploymentResult_AllSuccess(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	phase.nodesToJoin = bkenode.Nodes{
		{IP: "192.168.1.1", Hostname: "node1"},
	}

	successNodes := bkenode.Nodes{{IP: "192.168.1.1", Hostname: "node1"}}
	failedNodes := bkenode.Nodes{}

	err := phase.determineDeploymentResult(successNodes, failedNodes, nil)
	assert.NoError(t, err)
}

func TestEnsureWorkerJoin_DetermineDeploymentResult_SomeSuccess(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	phase.nodesToJoin = bkenode.Nodes{
		{IP: "192.168.1.1", Hostname: "node1"},
		{IP: "192.168.1.2", Hostname: "node2"},
	}

	successNodes := bkenode.Nodes{{IP: "192.168.1.1", Hostname: "node1"}}
	failedNodes := bkenode.Nodes{{IP: "192.168.1.2", Hostname: "node2"}}

	err := phase.determineDeploymentResult(successNodes, failedNodes, nil)
	assert.NoError(t, err)
}

func TestEnsureWorkerJoin_DetermineDeploymentResult_AllFailed(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	phase.nodesToJoin = bkenode.Nodes{
		{IP: "192.168.1.1", Hostname: "node1"},
	}

	successNodes := bkenode.Nodes{}
	failedNodes := bkenode.Nodes{{IP: "192.168.1.1", Hostname: "node1"}}

	err := phase.determineDeploymentResult(successNodes, failedNodes, assert.AnError)
	assert.Error(t, err)
}

func TestEnsureWorkerJoin_WaitWorkerJoin_EmptyNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(bkeCluster).Build()

	ctx := &phaseframe.PhaseContext{
		BKECluster: bkeCluster,
		Client:     c,
		Scheme:     scheme,
		Context:    context.Background(),
		Log:        bkev1beta1.NewBKELogger(nil, &fakeRecorder{}, bkeCluster),
	}

	phase := NewEnsureWorkerJoin(ctx).(*EnsureWorkerJoin)
	phase.nodesToJoin = nil

	err := phase.waitWorkerJoin()
	assert.NoError(t, err)
}

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
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/kube"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/mergecluster"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/phaseframe/phaseutil"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/statusmanage"
)

type fakeClient struct {
	client.Client
}

type fakeRecorder struct{}

func (f *fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {}
func (f *fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}
func (f *fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (f *fakeClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return nil
}

func (f *fakeClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return nil
}

func (f *fakeClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return nil
}

func (f *fakeClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return nil
}

func createTestLogger() *bkev1beta1.BKELogger {
	sugar := zap.NewNop().Sugar()
	bkeCluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	return bkev1beta1.NewBKELogger(sugar, &fakeRecorder{}, bkeCluster)
}

func TestCommonFunctions(t *testing.T) {
	assert.Equal(t, 2, MasterInitSleepSeconds)
}

func TestProcessNodeMachineMapping_EmptyNodes(t *testing.T) {
	logger := createTestLogger()
	params := ProcessNodeMachineMappingParams{
		Ctx:        context.Background(),
		BKECluster: &bkev1beta1.BKECluster{},
		Nodes:      []confv1beta1.Node{},
		Log:        logger,
	}

	result, err := ProcessNodeMachineMapping(params)
	assert.NoError(t, err)
	assert.Empty(t, result.DeleteMap)
	assert.Empty(t, result.WaitDeleteMap)
	assert.Equal(t, 0, result.NodesCount)
}

func TestProcessCommandFailure_RefreshError(t *testing.T) {
	params := ProcessCommandFailureParams{
		RefreshContext: func() error {
			return assert.AnError
		},
	}

	result := ProcessCommandFailure(params)
	assert.False(t, result.Done)
	assert.False(t, result.Success)
	assert.Error(t, result.Err)
}

func TestProcessNodeMachineMapping_WithNodes(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	logger := createTestLogger()
	params := ProcessNodeMachineMappingParams{
		Ctx:        context.Background(),
		Client:     &fakeClient{},
		BKECluster: &bkev1beta1.BKECluster{},
		Nodes:      []confv1beta1.Node{{IP: "192.168.1.1"}},
		Log:        logger,
	}

	patches.ApplyFunc(phaseutil.NodeToMachine, func(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, node confv1beta1.Node) (*clusterv1.Machine, error) {
		return &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine1"}}, nil
	})

	result, err := ProcessNodeMachineMapping(params)
	assert.NoError(t, err)
	assert.Len(t, result.DeleteMap, 1)
}

func TestProcessNodeMachineMapping_NilParams(t *testing.T) {
	logger := createTestLogger()
	params := ProcessNodeMachineMappingParams{
		Ctx:        context.Background(),
		BKECluster: &bkev1beta1.BKECluster{},
		Nodes:      nil,
		Log:        logger,
	}

	result, err := ProcessNodeMachineMapping(params)
	assert.NoError(t, err)
	assert.Equal(t, 0, result.NodesCount)
}

func TestProcessCommandFailureParams_Structure(t *testing.T) {
	params := ProcessCommandFailureParams{
		Context:    context.Background(),
		BKECluster: &bkev1beta1.BKECluster{},
		InitCommand: &agentv1beta1.Command{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		},
	}
	assert.NotNil(t, params.Context)
	assert.NotNil(t, params.BKECluster)
	assert.NotNil(t, params.InitCommand)
}

func TestProcessNodeMachineMappingResult_Structure(t *testing.T) {
	result := ProcessNodeMachineMappingResult{
		DeleteMap: map[string]phaseutil.MachineAndNode{
			"node1": {Machine: &clusterv1.Machine{}},
		},
		WaitDeleteMap: map[string]phaseutil.MachineAndNode{
			"node2": {Machine: &clusterv1.Machine{}},
		},
		NodesCount: 2,
	}
	assert.Len(t, result.DeleteMap, 1)
	assert.Len(t, result.WaitDeleteMap, 1)
	assert.Equal(t, 2, result.NodesCount)
}

func TestProcessCommandFailureResult_Structure(t *testing.T) {
	result := ProcessCommandFailureResult{
		Done:    true,
		Success: false,
		Err:     assert.AnError,
	}
	assert.True(t, result.Done)
	assert.False(t, result.Success)
	assert.Error(t, result.Err)
}

func TestProcessNodeMachineMapping_NodeFetcherNil(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	logger := createTestLogger()
	params := ProcessNodeMachineMappingParams{
		Ctx:               context.Background(),
		Client:            &fakeClient{},
		BKECluster:        &bkev1beta1.BKECluster{},
		NodeFetcher:       nil,
		Nodes:             []confv1beta1.Node{{IP: "192.168.1.1"}},
		Log:               logger,
		NodeDeletedReason: "NodeDeleted",
		NodeJoinedReason:  "NodeJoined",
	}

	patches.ApplyFunc(phaseutil.NodeToMachine, func(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, node confv1beta1.Node) (*clusterv1.Machine, error) {
		return nil, assert.AnError
	})

	patches.ApplyMethod(&statusmanage.StatusManager{}, "RemoveSingleNodeStatusCache", func(_ *statusmanage.StatusManager, bkeCluster *bkev1beta1.BKECluster, nodeIP string) {
	})

	patches.ApplyFunc(phaseutil.RemoveAppointmentDeletedNodes, func(cluster *bkev1beta1.BKECluster, nodeIP string) {
	})

	patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(c client.Client, bkeCluster *bkev1beta1.BKECluster, patchs ...mergecluster.PatchFunc) error {
		return nil
	})

	result, err := ProcessNodeMachineMapping(params)
	assert.NoError(t, err)
	assert.Empty(t, result.DeleteMap)
}

func TestProcessNodeMachineMapping_SyncStatusError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	logger := createTestLogger()
	params := ProcessNodeMachineMappingParams{
		Ctx:               context.Background(),
		Client:            &fakeClient{},
		BKECluster:        &bkev1beta1.BKECluster{},
		NodeFetcher:       nil,
		Nodes:             []confv1beta1.Node{{IP: "192.168.1.1"}},
		Log:               logger,
		NodeDeletedReason: "NodeDeleted",
		NodeJoinedReason:  "NodeJoined",
	}

	patches.ApplyFunc(phaseutil.NodeToMachine, func(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, node confv1beta1.Node) (*clusterv1.Machine, error) {
		return nil, assert.AnError
	})

	patches.ApplyMethod(&statusmanage.StatusManager{}, "RemoveSingleNodeStatusCache", func(_ *statusmanage.StatusManager, bkeCluster *bkev1beta1.BKECluster, nodeIP string) {
	})

	patches.ApplyFunc(phaseutil.RemoveAppointmentDeletedNodes, func(cluster *bkev1beta1.BKECluster, nodeIP string) {
	})

	patches.ApplyFunc(mergecluster.SyncStatusUntilComplete, func(c client.Client, bkeCluster *bkev1beta1.BKECluster, patchs ...mergecluster.PatchFunc) error {
		return assert.AnError
	})

	result, err := ProcessNodeMachineMapping(params)
	assert.Error(t, err)
	assert.Empty(t, result.DeleteMap)
}

func TestProcessNodeMachineMapping_MachineDeleting(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	logger := createTestLogger()
	params := ProcessNodeMachineMappingParams{
		Ctx:               context.Background(),
		Client:            &fakeClient{},
		BKECluster:        &bkev1beta1.BKECluster{},
		Nodes:             []confv1beta1.Node{{IP: "192.168.1.1"}},
		Log:               logger,
		NodeDeletedReason: "NodeDeleted",
	}

	patches.ApplyFunc(phaseutil.NodeToMachine, func(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, node confv1beta1.Node) (*clusterv1.Machine, error) {
		return &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "machine1"},
			Status: clusterv1.MachineStatus{
				Phase: string(clusterv1.MachinePhaseDeleting),
			},
		}, nil
	})

	result, err := ProcessNodeMachineMapping(params)
	assert.NoError(t, err)
	assert.Len(t, result.WaitDeleteMap, 1)
	assert.Empty(t, result.DeleteMap)
}

func TestProcessNodeMachineMapping_MachineDeleted(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	logger := createTestLogger()
	params := ProcessNodeMachineMappingParams{
		Ctx:               context.Background(),
		Client:            &fakeClient{},
		BKECluster:        &bkev1beta1.BKECluster{},
		Nodes:             []confv1beta1.Node{{IP: "192.168.1.1"}},
		Log:               logger,
		NodeDeletedReason: "NodeDeleted",
	}

	patches.ApplyFunc(phaseutil.NodeToMachine, func(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster, node confv1beta1.Node) (*clusterv1.Machine, error) {
		return &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "machine1"},
			Status: clusterv1.MachineStatus{
				Phase: string(clusterv1.MachinePhaseDeleted),
			},
		}, nil
	})

	result, err := ProcessNodeMachineMapping(params)
	assert.NoError(t, err)
	assert.Empty(t, result.WaitDeleteMap)
	assert.Empty(t, result.DeleteMap)
}

func TestGetTargetClusterNodes_GetClientError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(kube.GetTargetClusterClient, func(ctx context.Context, c client.Client, bkeCluster *bkev1beta1.BKECluster) (*kubernetes.Clientset, dynamic.Interface, error) {
		return nil, nil, assert.AnError
	})

	nodes, err := GetTargetClusterNodes(context.Background(), &fakeClient{}, &bkev1beta1.BKECluster{})
	assert.Error(t, err)
	assert.Nil(t, nodes)
}



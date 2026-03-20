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

package phaseutil

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkeagent/v1beta1"
	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/annotation"
)

func TestIsPaused(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{Pause: true},
	}
	annotation.SetAnnotation(cluster, annotation.BKEClusterPauseAnnotationKey, "true")

	result := IsPaused(cluster)
	assert.True(t, result)
}

func TestIsPaused_NotPaused(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{Pause: false},
	}
	result := IsPaused(cluster)
	assert.True(t, result)
}

func TestIsDeleteOrReset(t *testing.T) {
	now := metav1.Now()
	cluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
	}

	result := IsDeleteOrReset(cluster)
	assert.True(t, result)
}

func TestIsDeleteOrReset_Reset(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{Reset: true},
	}

	result := IsDeleteOrReset(cluster)
	assert.True(t, result)
}

func TestGenerateBKEAgentStatus(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	nodes := node.Nodes{{IP: "192.168.1.1"}, {IP: "192.168.1.2"}}
	success := []string{"192.168.1.1"}

	GenerateBKEAgentStatus(success, cluster, nodes)
	assert.Equal(t, int32(2), cluster.Status.AgentStatus.Replies)
	assert.Equal(t, int32(1), cluster.Status.AgentStatus.UnavailableReplies)
}

func TestGetListFiltersByBKECluster(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	filters := GetListFiltersByBKECluster(cluster)
	assert.Equal(t, 2, len(filters))
}

func TestGetBootTimeOut_NotFound(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	timeout, err := GetBootTimeOut(cluster)
	assert.Error(t, err)
	assert.Equal(t, 10*time.Minute, timeout)
}

func TestGetBootTimeOut_WithAnnotation(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	annotation.SetAnnotation(cluster, annotation.NodeBootWaitTimeOutAnnotationKey, "5m")
	timeout, err := GetBootTimeOut(cluster)
	assert.NoError(t, err)
	assert.Equal(t, 5*time.Minute, timeout)
}

func TestGetBKEClusterAssociateMachines(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clusterv1.AddToScheme(scheme)
	_ = bkev1beta1.AddToScheme(scheme)

	cluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-machine", Namespace: "default",
			Labels: map[string]string{clusterv1.ClusterNameLabel: "test"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(machine).Build()

	machines, err := GetBKEClusterAssociateMachines(context.Background(), c, cluster)
	assert.NoError(t, err)
	assert.Len(t, machines, 1)
}

func TestGetBKEClusterAssociateMasterMachines(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clusterv1.AddToScheme(scheme)
	_ = bkev1beta1.AddToScheme(scheme)

	cluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-machine", Namespace: "default",
			Labels: map[string]string{
				clusterv1.ClusterNameLabel:        "test",
				clusterv1.MachineControlPlaneLabel: "",
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(machine).Build()

	machines, err := GetBKEClusterAssociateMasterMachines(context.Background(), c, cluster)
	assert.NoError(t, err)
	assert.Len(t, machines, 1)
}

func TestGetBKEClusterAssociateWorkerMachines(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clusterv1.AddToScheme(scheme)
	_ = bkev1beta1.AddToScheme(scheme)

	cluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-machine", Namespace: "default",
			Labels: map[string]string{clusterv1.ClusterNameLabel: "test"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(machine).Build()

	machines, err := GetBKEClusterAssociateWorkerMachines(context.Background(), c, cluster)
	assert.NoError(t, err)
	assert.Len(t, machines, 1)
}

func TestGetBKEClusterAssociateBKEMachines(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)

	cluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	bkeMachine := &bkev1beta1.BKEMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-bke", Namespace: "default",
			Labels: map[string]string{clusterv1.ClusterNameLabel: "test"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bkeMachine).Build()

	machines, err := GetBKEClusterAssociateBKEMachines(context.Background(), c, cluster)
	assert.NoError(t, err)
	assert.Len(t, machines, 1)
}

func TestGetBKEClusterAssociateCommands(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = agentv1beta1.AddToScheme(scheme)
	_ = bkev1beta1.AddToScheme(scheme)

	cluster := &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	commands, err := GetBKEClusterAssociateCommands(context.Background(), c, cluster)
	assert.NoError(t, err)
	assert.Len(t, commands, 0)
}

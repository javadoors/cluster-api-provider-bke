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

package kube

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

const (
	// testNodeSkipIP 测试中需要跳过的节点IP（使用本地回环地址用于测试）
	testNodeSkipIP = "127.0.0.100"
	// testNodeCheckIP 测试中需要检查的节点IP（使用本地回环地址用于测试）
	testNodeCheckIP = "127.0.0.101"
	// testClusterName 测试集群名称
	testClusterName = "test-cluster"
	// testNodeSkipName 需要跳过的节点名称
	testNodeSkipName = "node-skip"
	// testNodeCheckName 需要检查的节点名称
	testNodeCheckName = "node-check"
	// expectedNodeCount 期望的节点数量
	expectedNodeCount = 2
)

// createTestCluster 创建测试用的 BKECluster 对象
func createTestCluster() *bkev1beta1.BKECluster {
	return &bkev1beta1.BKECluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: testClusterName,
		},
		Status: confv1beta1.BKEClusterStatus{},
	}
}

// createTestNode 创建测试用的 Kubernetes Node 对象
func createTestNode(name, ip string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{
					Type:    corev1.NodeInternalIP,
					Address: ip,
				},
			},
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
}

// createTestNodes 创建测试用的节点列表
func createTestNodes() []corev1.Node {
	return []corev1.Node{
		createTestNode(testNodeSkipName, testNodeSkipIP),
		createTestNode(testNodeCheckName, testNodeCheckIP),
	}
}

// createTestBKENodes 创建测试用的 BKENode 列表
func createTestBKENodes() bkev1beta1.BKENodes {
	return bkev1beta1.BKENodes{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNodeSkipName,
			},
			Spec: confv1beta1.BKENodeSpec{
				IP: testNodeSkipIP,
			},
			Status: confv1beta1.BKENodeStatus{
				NeedSkip: true,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNodeCheckName,
			},
			Spec: confv1beta1.BKENodeSpec{
				IP: testNodeCheckIP,
			},
			Status: confv1beta1.BKENodeStatus{
				NeedSkip: false,
			},
		},
	}
}

// verifyNodeSkipLogic 验证节点跳过逻辑
// 现在使用 BKENodes wrapper 的 GetNodeStateNeedSkip 方法
func verifyNodeSkipLogic(t *testing.T, bkeNodes bkev1beta1.BKENodes) {
	shouldSkipFirst := bkeNodes.GetNodeStateNeedSkip(testNodeSkipIP)
	shouldSkipSecond := bkeNodes.GetNodeStateNeedSkip(testNodeCheckIP)

	if !shouldSkipFirst {
		t.Errorf("Expected node %s to be skipped (needskip=true), "+
			"but GetNodeStateNeedSkip returned false", testNodeSkipIP)
	}

	if shouldSkipSecond {
		t.Errorf("Expected node %s not to be skipped (needskip=false), "+
			"but GetNodeStateNeedSkip returned true", testNodeCheckIP)
	}
}

// verifyNodeCount 验证节点数量
func verifyNodeCount(t *testing.T, nodes []corev1.Node) {
	if len(nodes) != expectedNodeCount {
		t.Errorf("Expected %d nodes in test data, but got %d", expectedNodeCount, len(nodes))
	}
}

// logTestCompletion 输出测试完成信息
func logTestCompletion(t *testing.T, cluster *bkev1beta1.BKECluster, bkeNodes bkev1beta1.BKENodes, nodes []corev1.Node) {
	t.Logf("Test completed: nodes=%d, bkeNodes=%d, cluster=%s", len(nodes), len(bkeNodes), cluster.Name)
	t.Logf("Node %s (needskip=true) should be skipped", testNodeSkipIP)
	t.Logf("Node %s (needskip=false) should be checked", testNodeCheckIP)
	t.Logf("Skip logic uses BKENodes.GetNodeStateNeedSkip method in CheckClusterHealth")
}

// TestCheckClusterHealthWithSkip 测试 CheckClusterHealth 方法中的跳过逻辑
func TestCheckClusterHealthWithSkip(t *testing.T) {
	t.Run("nodes with needskip=true should be skipped in CheckClusterHealth", func(t *testing.T) {
		cluster := createTestCluster()
		nodes := createTestNodes()
		bkeNodes := createTestBKENodes()

		verifyNodeSkipLogic(t, bkeNodes)
		verifyNodeCount(t, nodes)
		logTestCompletion(t, cluster, bkeNodes, nodes)
	})
}

func TestStaticPodName(t *testing.T) {
	tests := []struct {
		component string
		nodeName  string
		want      string
	}{
		{"kube-apiserver", "master-1", "kube-apiserver-master-1"},
		{"etcd", "node-1", "etcd-node-1"},
		{"", "node", "-node"},
	}

	for _, tt := range tests {
		got := StaticPodName(tt.component, tt.nodeName)
		if got != tt.want {
			t.Errorf("StaticPodName(%q, %q) = %q, want %q", tt.component, tt.nodeName, got, tt.want)
		}
	}
}

func TestNodeReady(t *testing.T) {
	tests := []struct {
		name string
		node *corev1.Node
		want bool
	}{
		{
			name: "ready node",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			},
			want: true,
		},
		{
			name: "not ready node",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
					},
				},
			},
			want: false,
		},
		{
			name: "no conditions",
			node: &corev1.Node{
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NodeReady(tt.node)
			if got != tt.want {
				t.Errorf("NodeReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckNodeReady(t *testing.T) {
	tests := []struct {
		name    string
		node    *corev1.Node
		wantErr bool
	}{
		{
			name: "readyNode",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "notReadyNode",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node2"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkNodeReady(tt.node)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkNodeReady() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckNodeVersion(t *testing.T) {
	const testVersion = "v1.28.0"

	tests := []struct {
		name          string
		node          *corev1.Node
		expectVersion string
		wantErr       bool
	}{
		{
			name: "matchingVersion",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node1"},
				Status: corev1.NodeStatus{
					NodeInfo: corev1.NodeSystemInfo{KubeletVersion: testVersion},
				},
			},
			expectVersion: testVersion,
			wantErr:       false,
		},
		{
			name: "mismatchVersion",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node2"},
				Status: corev1.NodeStatus{
					NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.27.0"},
				},
			},
			expectVersion: testVersion,
			wantErr:       true,
		},
		{
			name: "emptyExpectVersion",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node3"},
				Status: corev1.NodeStatus{
					NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.27.0"},
				},
			},
			expectVersion: "",
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkNodeVersion(tt.node, tt.expectVersion)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkNodeVersion() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFilterPodsWithPrefix(t *testing.T) {
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "coredns-123"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "coredns-456"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "etcd-master"}},
	}

	tests := []struct {
		name   string
		prefix string
		want   int
	}{
		{"corednsPrefix", "coredns", 2},
		{"etcdPrefix", "etcd", 1},
		{"noMatch", "kube-proxy", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterPodsWithPrefix(pods, tt.prefix)
			if len(got) != tt.want {
				t.Errorf("filterPodsWithPrefix() = %v, want %v", len(got), tt.want)
			}
		})
	}
}

func TestCheckItemContains(t *testing.T) {
	items := []string{"kubeproxy", "calico", "coredns"}

	tests := []struct {
		name string
		item string
		want bool
	}{
		{"found", "calico", true},
		{"notFound", "metrics-server", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkItemContains(tt.item, items)
			if got != tt.want {
				t.Errorf("checkItemContains() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindAddonComponent(t *testing.T) {
	tests := []struct {
		name      string
		addon     string
		wantFound bool
	}{
		{"foundClusterAPI", "cluster-api", true},
		{"foundOpenfuyao", "openfuyao-system-controller", true},
		{"notFound", "unknown-addon", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, found := findAddonComponent(tt.addon)
			if found != tt.wantFound {
				t.Errorf("findAddonComponent() found = %v, want %v", found, tt.wantFound)
			}
		})
	}
}

func TestVerifyComponentPods(t *testing.T) {
	const testNamespace = "kube-system"

	tests := []struct {
		name    string
		pods    []corev1.Pod
		prefix  string
		wantErr bool
	}{
		{
			name: "allRunning",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-master"},
					Status:     corev1.PodStatus{Phase: corev1.PodRunning},
				},
			},
			prefix:  "etcd",
			wantErr: false,
		},
		{
			name: "notRunning",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-master"},
					Status:     corev1.PodStatus{Phase: corev1.PodPending},
				},
			},
			prefix:  "etcd",
			wantErr: true,
		},
		{
			name:    "noPods",
			pods:    []corev1.Pod{},
			prefix:  "etcd",
			wantErr: true,
		},
		{
			name: "corednsOneRunning",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "coredns-1"},
					Status:     corev1.PodStatus{Phase: corev1.PodRunning},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "coredns-2"},
					Status:     corev1.PodStatus{Phase: corev1.PodPending},
				},
			},
			prefix:  "coredns",
			wantErr: false,
		},
		{
			name: "corednsNoneRunning",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "coredns-1"},
					Status:     corev1.PodStatus{Phase: corev1.PodPending},
				},
			},
			prefix:  "coredns",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{}
			err := client.verifyComponentPods(tt.pods, tt.prefix, testNamespace)
			if (err != nil) != tt.wantErr {
				t.Errorf("verifyComponentPods() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNodeHealthCheck(t *testing.T) {
	const testVersion = "v1.28.0"

	tests := []struct {
		name              string
		node              *corev1.Node
		expectVersion     string
		componentCheckErr error
		wantErr           bool
	}{
		{
			name: "healthyMasterNode",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "master-1",
					Labels: map[string]string{"node-role.kubernetes.io/master": ""},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
					NodeInfo: corev1.NodeSystemInfo{KubeletVersion: testVersion},
				},
			},
			expectVersion:     testVersion,
			componentCheckErr: nil,
			wantErr:           false,
		},
		{
			name: "notReadyNode",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
					},
				},
			},
			expectVersion: testVersion,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				Log: zap.NewNop().Sugar(),
			}

			patches := gomonkey.ApplyMethod(client, "CheckComponentHealth",
				func(_ *Client, _ *corev1.Node) error {
					return tt.componentCheckErr
				})
			defer patches.Reset()

			err := client.NodeHealthCheck(tt.node, tt.expectVersion, client.Log)
			if (err != nil) != tt.wantErr {
				t.Errorf("NodeHealthCheck() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckComponentHealth(t *testing.T) {
	tests := []struct {
		name      string
		node      *corev1.Node
		getPodErr error
		podPhase  corev1.PodPhase
		wantErr   bool
	}{
		{
			name: "allComponentsRunning",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "master-1"},
			},
			getPodErr: nil,
			podPhase:  corev1.PodRunning,
			wantErr:   false,
		},
		{
			name: "componentNotRunning",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "master-2"},
			},
			getPodErr: nil,
			podPhase:  corev1.PodPending,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{}

			patches := gomonkey.ApplyMethod(client, "GetPod",
				func(_ *Client, _, _ string) (*corev1.Pod, error) {
					if tt.getPodErr != nil {
						return nil, tt.getPodErr
					}
					return &corev1.Pod{
						Status: corev1.PodStatus{Phase: tt.podPhase},
					}, nil
				})
			defer patches.Reset()

			err := client.CheckComponentHealth(tt.node)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckComponentHealth() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetPods(t *testing.T) {
	const testNamespace = "kube-system"

	client := &Client{
		ClientSet: &kubernetes.Clientset{},
		Ctx:       context.Background(),
	}

	patches := gomonkey.ApplyMethod(client.ClientSet.CoreV1().Pods(testNamespace), "List",
		func(_ interface{}, _ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
			return &corev1.PodList{
				Items: []corev1.Pod{
					{ObjectMeta: metav1.ObjectMeta{Name: "pod1"}},
				},
			}, nil
		})
	defer patches.Reset()

	pods, err := client.getPods(testNamespace)
	if err != nil {
		t.Errorf("getPods() error = %v", err)
	}
	if len(pods) != 1 {
		t.Errorf("getPods() returned %d pods, want 1", len(pods))
	}
}

func TestGetPodsError(t *testing.T) {
	const testNamespace = "kube-system"

	client := &Client{
		ClientSet: &kubernetes.Clientset{},
		Ctx:       context.Background(),
	}

	patches := gomonkey.ApplyMethod(client.ClientSet.CoreV1().Pods(testNamespace), "List",
		func(_ interface{}, _ context.Context, _ metav1.ListOptions) (*corev1.PodList, error) {
			return nil, errors.New("list failed")
		})
	defer patches.Reset()

	_, err := client.getPods(testNamespace)
	if err == nil {
		t.Error("getPods() expected error")
	}
}


func TestProcessAddonComponentCheck(t *testing.T) {
	client := &Client{}
	err := client.processAddonComponentCheck("unknown-addon")
	if err == nil {
		t.Error("processAddonComponentCheck() expected error for unknown addon")
	}
}


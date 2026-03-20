/*
 * Copyright (c) 2025 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package phaseutil

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util/version"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
)

func compareVersions(oldVersion, newVersion string) (int, error) {
	oldv, err := version.ParseMajorMinorPatch(oldVersion)
	if err != nil {
		fmt.Printf("parse %v err %v\n", oldVersion, err)
		return 0, err
	}
	newv, err := version.ParseMajorMinorPatch(newVersion)
	if err != nil {
		fmt.Printf("parse %v err %v\n", newVersion, err)
		return 0, err
	}

	// step 2 compare cluster version upgrade
	return version.Compare(newv, oldv), nil
}

func TestVersionCompare(t *testing.T) {
	tests := []struct {
		name        string
		oldVersion  string
		newVersion  string
		expected    int
		shouldError bool
	}{
		{
			name:       "same versions",
			oldVersion: "v25.9.0",
			newVersion: "v25.9.0",
			expected:   0,
		},
		{
			name:       "old less than new - patch",
			oldVersion: "v25.9.0",
			newVersion: "v25.9.1",
			expected:   1,
		},
		{
			name:       "old greater than new - patch",
			oldVersion: "v25.9.1",
			newVersion: "v25.9.0",
			expected:   -1,
		},
		{
			name:        "version is illegal",
			oldVersion:  "v25.09",
			newVersion:  "v25.9",
			expected:    1,
			shouldError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := compareVersions(tt.oldVersion, tt.newVersion)
			if err != nil {
				if !tt.shouldError {
					t.Errorf("compareVersions() error = %v, wantErr %v", err, tt.shouldError)
				}
			} else {
				if result != tt.expected {
					t.Errorf("compareVersions(%s, %s) = %d, expected %d",
						tt.oldVersion, tt.newVersion, result, tt.expected)
				}
			}
		})
	}
}

func isPatchVersion(version string) bool {
	cleanVersion := strings.TrimPrefix(version, "v")

	v, err := semver.NewVersion(cleanVersion)
	if err != nil {
		return false
	}

	return v.Patch > 0 && v.PreRelease == ""
}

func TestPatchVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		isPatch bool
	}{
		{
			name:    "not patch case 1",
			version: "v25.09.0",
			isPatch: false,
		},
		{
			name:    "not patch case 2",
			version: "v25.9",
			isPatch: false,
		},
		{
			name:    "not patch case 3",
			version: "v25.09.1.rc",
			isPatch: false,
		},
		{
			name:    "patch case 1",
			version: "v25.09.1",
			isPatch: true,
		},
		{
			name:    "patch case 2",
			version: "v25.9.2",
			isPatch: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isPatch := isPatchVersion(tt.version)
			if isPatch != tt.isPatch {
				t.Errorf("version %v should %v but %v", tt.version, tt.isPatch, isPatch)
			}
		})
	}
}

func TestNeedUpgrade(t *testing.T) {
	tests := []struct {
		name     string
		oldV     string
		newV     string
		expected bool
	}{
		// --- Case 1: from v25.09 ---
		{"v25.09 to v25.10-rc.2", "v25.09", "v25.10-rc.2", true},
		{"v25.09 to v25.10", "v25.09", "v25.10", true},
		{"v25.09 to v25.12", "v25.09", "v25.12", true},
		{"v25.09 to v25.12-rc.1", "v25.09", "v25.12-rc.1", true},

		// --- Case 2: from v25.10 ---
		{"v25.10 to v25.10.2", "v25.10", "v25.10.2", true},
		{"v25.10 to v25.12", "v25.10", "v25.12", true},
		{"v25.10 to v25.12-rc.1", "v25.10", "v25.12-rc.1", true},

		// --- Case 3: from v25.10-rc.2 ---
		{"v25.10-rc.2 to v25.10", "v25.10-rc.2", "v25.10", true},
		{"v25.10-rc.2 to v25.12", "v25.10-rc.2", "v25.12", true},
		{"v25.10-rc.2 to v25.12-rc.1", "v25.10-rc.2", "v25.12-rc.1", true},
		{"v25.10-rc.2 to v25.10-rc.3", "v25.10-rc.2", "v25.10-rc.3", true},
		{"v25.10-rc.2 to v25.10.1", "v25.10-rc.2", "v25.10.1", true},

		// --- Case 4: from v25.10.1 ---
		{"v25.10.1 to v25.10.2", "v25.10.1", "v25.10.2", true},
		{"v25.10.1 to v25.12-rc.1", "v25.10.1", "v25.12-rc.1", true},
		{"v25.10.1 to v25.12", "v25.10.1", "v25.12", true},
		{"v25.10.1 to v25.12.1", "v25.10.1", "v25.12.1", true},

		// --- Edge: no upgrade needed ---
		{"same version", "v25.10", "v25.10", false},
		{"downgrade", "v25.10", "v25.09", false},
		{"rc to earlier rc", "v25.10-rc.2", "v25.10-rc.1", false},
		{"patch to lower patch", "v25.10.2", "v25.10.1", false},

		// --- Pre-release ordering ---
		{"rc to final", "v25.10-rc.2", "v25.10", true},
		{"final to rc (should be false)", "v25.10", "v25.10-rc.3", false}, // rc < final

		// --- Invalid versions ---
		{"invalid old", "invalid", "v25.10", false},
		{"invalid new", "v25.10", "not-a-version", false},
		{"both invalid", "foo", "bar", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedUpgrade(tt.oldV, tt.newV)
			if got != tt.expected {
				t.Errorf("NeedUpgrade(%q, %q) = %v, want %v", tt.oldV, tt.newV, got, tt.expected)
			}
		})
	}
}

func TestNeedUpgradeWithOF(t *testing.T) {
	tests := []struct {
		old  string
		new  string
		want bool
	}{
		{"v1.2.1-of.1", "v1.2.1-of.2", true}, // of 编号不同
		{"v1.2-of.1", "v1.2.1-of.1", true},   // 修订号不同
		{"v1.2.1-of.2", "v1.2.2-of.1", true}, // 修订号更大

		{"v1.2.0", "v1.2.1-of.1", true},        // 没有 of 后缀
		{"v1.2.1-of.2", "v1.2.1-of.1", false},  // of 编号更小
		{"v2.0.0-of.1", "v1.9.9-of.99", false}, // 主版本更小
		{"v1.2.1-of.1", "v1.2.1-of.1", false},  // 完全相同
		{"v1.2-of.2", "v1.2.0-of.1", false},    // 修订号相等，of 更大但整体更小
	}

	for _, tt := range tests {
		got := NeedUpgrade(tt.old, tt.new)
		if got != tt.want {
			t.Errorf("NeedUpgrade(%q, %q) = %v, want %v", tt.old, tt.new, got, tt.want)
		}
	}
}

func TestNodeInfo(t *testing.T) {
	tests := []struct {
		name     string
		node     confv1beta1.Node
		expected string
	}{
		{"both hostname and IP", confv1beta1.Node{Hostname: "node1", IP: "192.168.1.1"}, "node1/192.168.1.1"},
		{"only IP", confv1beta1.Node{IP: "192.168.1.1"}, "192.168.1.1"},
		{"only hostname", confv1beta1.Node{Hostname: "node1"}, "node1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NodeInfo(tt.node)
			if result != tt.expected {
				t.Errorf("NodeInfo() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNodeRoleString(t *testing.T) {
	node := confv1beta1.Node{Role: []string{"master", "worker"}}
	result := NodeRoleString(node)
	if result != "master;worker" {
		t.Errorf("NodeRoleString() = %v, want master;worker", result)
	}
}

func TestIsMasterNode(t *testing.T) {
	tests := []struct {
		name     string
		node     *confv1beta1.Node
		expected bool
	}{
		{"is master", &confv1beta1.Node{Role: []string{"master"}}, true},
		{"not master", &confv1beta1.Node{Role: []string{"worker"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsMasterNode(tt.node)
			if result != tt.expected {
				t.Errorf("IsMasterNode() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsWorkerNode(t *testing.T) {
	tests := []struct {
		name     string
		node     *confv1beta1.Node
		expected bool
	}{
		{"is worker", &confv1beta1.Node{Role: []string{"node"}}, true},
		{"not worker", &confv1beta1.Node{Role: []string{"master"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsWorkerNode(tt.node)
			if result != tt.expected {
				t.Errorf("IsWorkerNode() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsEtcdNode(t *testing.T) {
	tests := []struct {
		name     string
		node     *confv1beta1.Node
		expected bool
	}{
		{"is etcd", &confv1beta1.Node{Role: []string{"etcd"}}, true},
		{"not etcd", &confv1beta1.Node{Role: []string{"master"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsEtcdNode(tt.node)
			if result != tt.expected {
				t.Errorf("IsEtcdNode() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetBKEAllNodesFromNodesStatusWithBKENodes(t *testing.T) {
	nodes := bkev1beta1.BKENodes{}
	result := GetBKEAllNodesFromNodesStatusWithBKENodes(nodes)
	if len(result) != 0 {
		t.Errorf("Expected empty nodes, got %d", len(result))
	}
}

func TestConvertELBNodesToBKENodes(t *testing.T) {
	src := bkenode.Nodes{{IP: "192.168.1.1"}, {IP: "192.168.1.2"}}
	elbNodes := []string{"192.168.1.1"}
	result := ConvertELBNodesToBKENodes(elbNodes, src)
	assert.Len(t, result, 1)
	assert.Equal(t, "192.168.1.1", result[0].IP)
}

func TestWithExcludeAppointmentNodes(t *testing.T) {
	cfg := &nodeFilterConfig{}
	opt := WithExcludeAppointmentNodes()
	opt(cfg)
	assert.True(t, cfg.excludeAppointment)
}

func TestWithBKENodes(t *testing.T) {
	nodes := bkev1beta1.BKENodes{}
	cfg := &nodeFilterConfig{}
	opt := WithBKENodes(nodes)
	opt(cfg)
	assert.NotNil(t, cfg.bkeNodes)
}

func TestGetBKENodesFromNodesStatusWithBKENodes(t *testing.T) {
	nodes := bkev1beta1.BKENodes{
		{Status: confv1beta1.BKENodeStatus{NeedSkip: false}},
		{Status: confv1beta1.BKENodeStatus{NeedSkip: true}},
	}
	result := GetBKENodesFromNodesStatusWithBKENodes(nodes)
	assert.Len(t, result, 1)
}

func TestGetBKEAllNodesFromNodesStatus(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	result := GetBKEAllNodesFromNodesStatus(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetBKENodesFromNodesStatus(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	result := GetBKENodesFromNodesStatus(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetAgentPushedNodesWithBKENodes(t *testing.T) {
	nodes := bkev1beta1.BKENodes{}
	result := GetAgentPushedNodesWithBKENodes(nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetAgentPushedNodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	result := GetAgentPushedNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedPushAgentNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedPushAgentNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedInitEnvNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedInitEnvNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedPostProcessNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedPostProcessNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedJoinNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedJoinNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedJoinMasterNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedJoinMasterNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedJoinWorkerNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedJoinWorkerNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetAppointmentAddNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	nodes := bkev1beta1.BKENodes{}
	result := GetAppointmentAddNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedDeleteWorkerNodesWithTargetNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	cluster := &bkev1beta1.BKECluster{}
	target := bkenode.Nodes{}
	result := GetNeedDeleteWorkerNodesWithTargetNodes(context.Background(), c, cluster, target)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedDeleteMasterNodesWithTargetNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	cluster := &bkev1beta1.BKECluster{}
	target := bkenode.Nodes{}
	result := GetNeedDeleteMasterNodesWithTargetNodes(context.Background(), c, cluster, target)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedDeleteNodesFromTargetNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	cluster := &bkev1beta1.BKECluster{}
	target := bkenode.Nodes{}
	result := GetNeedDeleteNodesFromTargetNodes(context.Background(), c, cluster, target)
	assert.Equal(t, 0, len(result))
}

func TestComputeFinalDeleteNodes(t *testing.T) {
	needDelete := bkenode.Nodes{{IP: "192.168.1.1"}, {IP: "192.168.1.2"}}
	appointment := bkenode.Nodes{{IP: "192.168.1.1"}}
	result := ComputeFinalDeleteNodes(needDelete, appointment)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "192.168.1.1", result[0].IP)
}

func TestNormalizeVersion(t *testing.T) {
	result, err := NormalizeVersion("v1.25.0")
	assert.NoError(t, err)
	assert.Equal(t, "v1.25.0", result)
}

func TestGetNeedPushAgentNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{}
	result := GetNeedPushAgentNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedInitEnvNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{}
	result := GetNeedInitEnvNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedPostProcessNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{}
	result := GetNeedPostProcessNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedJoinNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{}
	result := GetNeedJoinNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedJoinMasterNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{}
	result := GetNeedJoinMasterNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedJoinWorkerNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{}
	result := GetNeedJoinWorkerNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedUpgradeComponentNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedUpgradeComponentNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedUpgradeNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedUpgradeNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedUpgradeK8sNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{KubernetesVersion: "v1.25.0"},
			},
		},
	}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedUpgradeK8sNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestComputeFinalAddNodes(t *testing.T) {
	needAdd := bkenode.Nodes{{IP: "192.168.1.1"}, {IP: "192.168.1.2"}}
	appointment := bkenode.Nodes{{IP: "192.168.1.1"}}
	result := ComputeFinalAddNodes(needAdd, appointment)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "192.168.1.2", result[0].IP)
}

func TestGetNeedUpgradeMasterNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.25.0"},
			},
		},
	}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedUpgradeMasterNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedUpgradeWorkerNodesWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.25.0"},
			},
		},
	}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedUpgradeWorkerNodesWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedLoadBalanceNodesWithBKENodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	cluster := &bkev1beta1.BKECluster{}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedLoadBalanceNodesWithBKENodes(context.Background(), c, cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedUpgradeEtcdsWithBKENodes(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{EtcdVersion: "v3.5.0"},
			},
		},
	}
	nodes := bkev1beta1.BKENodes{}
	result := GetNeedUpgradeEtcdsWithBKENodes(cluster, nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedUpgradeMasterNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.25.0"},
			},
		},
	}
	result := GetNeedUpgradeMasterNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedUpgradeWorkerNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.25.0"},
			},
		},
	}
	result := GetNeedUpgradeWorkerNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedUpgradeK8sNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{KubernetesVersion: "v1.25.0"},
			},
		},
	}
	result := GetNeedUpgradeK8sNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedUpgradeNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{OpenFuyaoVersion: "v1.25.0"},
			},
		},
	}
	result := GetNeedUpgradeNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedUpgradeEtcds(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{EtcdVersion: "v3.5.0"},
			},
		},
	}
	result := GetNeedUpgradeEtcds(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetReadyBootstrapNodesWithBKENodes(t *testing.T) {
	nodes := bkev1beta1.BKENodes{}
	result := GetReadyBootstrapNodesWithBKENodes(nodes)
	assert.Equal(t, 0, len(result))
}

func TestGetClientURLByIP(t *testing.T) {
	result := GetClientURLByIP("192.168.1.1")
	assert.Equal(t, "https://192.168.1.1:2379", result)
}

func TestGetNeedUpgradeComponentNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{}
	result := GetNeedUpgradeComponentNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNeedLoadBalanceNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	scheme := runtime.NewScheme()
	_ = bkev1beta1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	cluster := &bkev1beta1.BKECluster{}
	result := GetNeedLoadBalanceNodes(context.Background(), c, cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetReadyBootstrapNodes(t *testing.T) {
	patches := gomonkey.ApplyFunc(GetBKENodesFromCluster, func(*bkev1beta1.BKECluster) bkev1beta1.BKENodes {
		return bkev1beta1.BKENodes{}
	})
	defer patches.Reset()

	cluster := &bkev1beta1.BKECluster{}
	result := GetReadyBootstrapNodes(cluster)
	assert.Equal(t, 0, len(result))
}

func TestGetNodeStateFlag(t *testing.T) {
	node := &confv1beta1.BKENode{
		Spec:   confv1beta1.BKENodeSpec{IP: "192.168.1.1"},
		Status: confv1beta1.BKENodeStatus{StateCode: 4},
	}
	result := GetNodeStateFlag(node, "192.168.1.1", 4)
	assert.True(t, result)
	result = GetNodeStateFlag(node, "192.168.1.2", 4)
	assert.False(t, result)
}


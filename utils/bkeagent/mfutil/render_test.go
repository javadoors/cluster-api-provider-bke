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

package mfutil

import (
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"k8s.io/kubernetes/cmd/kubeadm/app/util/etcd"
)

func TestMergeFuncMap(t *testing.T) {
	f1 := &template.FuncMap{
		"func1": func() string { return "func1" },
		"func2": func() string { return "func2" },
	}
	f2 := &template.FuncMap{
		"func3": func() string { return "func3" },
		"func4": func() string { return "func4" },
	}
	result := mergeFuncMap(f1, f2)

	assert.Equal(t, numFour, len(*result))
	assert.NotNil(t, (*result)["func1"])
	assert.NotNil(t, (*result)["func2"])
	assert.NotNil(t, (*result)["func3"])
	assert.NotNil(t, (*result)["func4"])
}

func TestMergeFuncMapOverwrite(t *testing.T) {
	f1 := &template.FuncMap{
		"func1": func() string { return "from_f1" },
	}
	f2 := &template.FuncMap{
		"func1": func() string { return "from_f2" },
	}
	result := mergeFuncMap(f1, f2)

	assert.Equal(t, numOne, len(*result))
}

func TestGetExtraArgs(t *testing.T) {
	argsMap := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}
	result := getExtraArgs(argsMap)
	assert.Len(t, result, numTwo)
	assert.Contains(t, result, "key1=value1")
	assert.Contains(t, result, "key2=value2")
}

func TestGetExtraArgsEmpty(t *testing.T) {
	argsMap := map[string]string{}
	result := getExtraArgs(argsMap)
	assert.Len(t, result, numZero)
}

func TestGetExtraArgsNil(t *testing.T) {
	result := getExtraArgs(nil)
	assert.Len(t, result, numZero)
}

func TestGlobalFuncMap(t *testing.T) {
	funcMap := GlobalFuncMap()
	assert.NotNil(t, funcMap)
	assert.NotNil(t, (*funcMap)["imageRepo"])
}

func TestUtilFuncMap(t *testing.T) {
	funcMap := utilFuncMap()
	assert.NotNil(t, funcMap)
	assert.NotNil(t, (*funcMap)["randomString"])
}

func TestKeepalivedConfFuncMap(t *testing.T) {
	funcMap := keepalivedConfFuncMap()
	assert.NotNil(t, funcMap)
	assert.NotNil(t, (*funcMap)["randomString"])
	assert.NotNil(t, (*funcMap)["computeWeight"])
	assert.NotNil(t, (*funcMap)["isMaster"])
	assert.NotNil(t, (*funcMap)["priority"])
}

func TestKeepalivedConfFuncMapRandomString(t *testing.T) {
	funcMap := keepalivedConfFuncMap()
	randomString := (*funcMap)["randomString"].(func(int) string)

	result := randomString(numTen)
	assert.Len(t, result, numTen)
}

func TestKeepalivedConfFuncMapComputeWeight(t *testing.T) {
	funcMap := keepalivedConfFuncMap()
	computeWeight := (*funcMap)["computeWeight"].(func([]HANode) string)

	nodes := []HANode{
		{IP: "192.168.1.1"},
		{IP: "192.168.1.2"},
	}
	result := computeWeight(nodes)
	assert.Equal(t, "20", result)
}

func TestKeepalivedConfFuncMapPriority(t *testing.T) {
	funcMap := keepalivedConfFuncMap()
	priority := (*funcMap)["priority"].(func([]HANode) string)

	nodes := []HANode{
		{IP: "192.168.1.1"},
		{IP: "192.168.1.2"},
	}
	result := priority(nodes)
	assert.NotEmpty(t, result)
}

func TestKeepalivedConfFuncMapPriorityWithEmptyNodes(t *testing.T) {
	funcMap := keepalivedConfFuncMap()
	priority := (*funcMap)["priority"].(func([]HANode) string)

	nodes := []HANode{}
	result := priority(nodes)
	assert.NotEmpty(t, result)
}

func TestControllerFuncMap(t *testing.T) {
	funcMap := controllerFuncMap()
	assert.NotNil(t, funcMap)
	assert.NotNil(t, (*funcMap)["imageInfo"])
	assert.NotNil(t, (*funcMap)["extraArgs"])
	assert.NotNil(t, (*funcMap)["getSubnetMask"])
}

func TestControllerFuncMapGetSubnetMask(t *testing.T) {
	funcMap := controllerFuncMap()
	getSubnetMask := (*funcMap)["getSubnetMask"].(func(string) string)

	tests := []struct {
		name     string
		cidr     string
		expected string
	}{
		{"cidr with mask 16", "10.0.0.0/16", "24"},
		{"cidr with mask 24", "10.0.0.0/24", "24"},
		{"cidr with mask 32", "10.0.0.0/32", "32"},
		{"cidr with mask less than 24", "10.0.0.0/20", "24"},
		{"cidr with mask 25", "10.0.0.0/25", "25"},
		{"invalid cidr", "invalid", ""},
		{"cidr without mask", "10.0.0.0", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getSubnetMask(tt.cidr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSchedulerFuncMap(t *testing.T) {
	funcMap := schedulerFuncMap()
	assert.NotNil(t, funcMap)
	assert.NotNil(t, (*funcMap)["imageInfo"])
	assert.NotNil(t, (*funcMap)["extraArgs"])
}

func TestEtcdFuncMap(t *testing.T) {
	funcMap := etcdFuncMap()
	assert.NotNil(t, funcMap)
	assert.NotNil(t, (*funcMap)["initialCluster"])
	assert.NotNil(t, (*funcMap)["imageInfo"])
	assert.NotNil(t, (*funcMap)["dataDir"])
	assert.NotNil(t, (*funcMap)["etcdAdvertiseUrls"])
	assert.NotNil(t, (*funcMap)["extraArgs"])
}

func TestEtcdFuncMapInitialClusterEmpty(t *testing.T) {
	funcMap := etcdFuncMap()
	initialCluster := (*funcMap)["initialCluster"].(func([]etcd.Member) string)

	members := []etcd.Member{}
	result := initialCluster(members)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "https://")
}

func TestEtcdFuncMapInitialClusterWithMembers(t *testing.T) {
	funcMap := etcdFuncMap()
	initialCluster := (*funcMap)["initialCluster"].(func([]etcd.Member) string)

	members := []etcd.Member{
		{Name: "etcd-1", PeerURL: "https://192.168.1.1:2380"},
		{Name: "etcd-2", PeerURL: "https://192.168.1.2:2380"},
	}
	result := initialCluster(members)
	assert.Contains(t, result, "etcd-1")
	assert.Contains(t, result, "etcd-2")
}

func TestApiServerFuncMap(t *testing.T) {
	funcMap := apiServerFuncMap()
	assert.NotNil(t, funcMap)
	assert.NotNil(t, (*funcMap)["advertiseAddress"])
	assert.NotNil(t, (*funcMap)["etcdServers"])
	assert.NotNil(t, (*funcMap)["apiServerPort"])
	assert.NotNil(t, (*funcMap)["imageInfo"])
	assert.NotNil(t, (*funcMap)["clientCAFile"])
	assert.NotNil(t, (*funcMap)["extraArgs"])
	assert.NotNil(t, (*funcMap)["upgradeWithOpenFuyao"])
}

func TestIsUpgradeWithOpenFuyao(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]interface{}
		want  bool
	}{
		{"with upgradeWithOpenFuyao true", map[string]interface{}{"upgradeWithOpenFuyao": true}, true},
		{"with upgradeWithOpenFuyao false", map[string]interface{}{"upgradeWithOpenFuyao": false}, false},
		{"without upgradeWithOpenFuyao", map[string]interface{}{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope := &BootScope{
				Extra: tt.extra,
			}
			result := isUpgradeWithOpenFuyao(scope)
			assert.Equal(t, tt.want, result)
		})
	}
}

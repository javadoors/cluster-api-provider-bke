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
	"testing"

	"github.com/stretchr/testify/assert"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
)

func TestIsValidSplitResult(t *testing.T) {
	assert.True(t, isValidSplitResult([]string{"a", "b"}))
	assert.False(t, isValidSplitResult([]string{"a"}))
}

func TestIsMatchingNode(t *testing.T) {
	node := bkenode.Node{IP: "192.168.1.1"}
	assert.True(t, isMatchingNode(node, "192.168.1.1"))
	assert.False(t, isMatchingNode(node, "192.168.1.2"))
	assert.False(t, isMatchingNode(node, ""))
}

func TestIsHostnameAlreadySet(t *testing.T) {
	nodes := []confv1beta1.BKENode{
		{Spec: confv1beta1.BKENodeSpec{Hostname: "host1"}},
		{Spec: confv1beta1.BKENodeSpec{Hostname: ""}},
	}
	assert.True(t, isHostnameAlreadySet(nodes, 0))
	assert.False(t, isHostnameAlreadySet(nodes, 1))
}

func TestUpdateNodeHostname(t *testing.T) {
	nodes := []confv1beta1.BKENode{
		{Spec: confv1beta1.BKENodeSpec{Hostname: ""}},
	}
	updateNodeHostname(nodes, 0, "newhost")
	assert.Equal(t, "newhost", nodes[0].Spec.Hostname)
}

func TestCleanupKeys(t *testing.T) {
	keys := []string{"a", "b", "c", "d"}
	result := cleanupKeys(keys, []int{1, 3})
	assert.Equal(t, []string{"a", "c"}, result)
}

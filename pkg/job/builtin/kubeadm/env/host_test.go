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

package env

import (
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
)

func TestExpectedBKENodeNameFromConfig(t *testing.T) {
	ep := &EnvPlugin{
		currenNode: bkenode.Node{Hostname: "master-01"},
	}
	assert.Equal(t, "master-01", ep.expectedBKENodeName())
}

func TestExpectedBKENodeNameFallback(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.HostName, func() string {
		return "from-node-file"
	})

	ep := &EnvPlugin{
		currenNode: bkenode.Node{},
	}
	assert.Equal(t, "from-node-file", ep.expectedBKENodeName())
}

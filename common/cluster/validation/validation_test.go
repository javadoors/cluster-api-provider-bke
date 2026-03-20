/*
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *           http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package validation

import (
	"bou.ke/monkey"
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/addon"
)

func TestValidateChartRepo(t *testing.T) {
	addons := addon.Addons{v1beta1.Product{Type: addon.ChartAddon}}

	// ip异常
	chartRepo := v1beta1.Repo{
		Domain: "",
		Ip:     "172.158.0.x",
		Port:   "8080",
		Prefix: "chart",
	}
	err := ValidateChartRepo(chartRepo, addons)
	assert.Error(t, err)

	// 无ip domain
	chartRepo = v1beta1.Repo{
		Domain: "",
		Ip:     "",
		Port:   "8080",
		Prefix: "chart",
	}
	err = ValidateChartRepo(chartRepo, addons)
	assert.Error(t, err)

	// 有ip domain
	chartRepo = v1beta1.Repo{
		Domain: "chart.domain",
		Ip:     "172.158.0.1",
		Port:   "8080",
		Prefix: "chart",
	}
	monkey.Patch(checkReachable, func(addr string) bool {
		return true
	})
	defer monkey.UnpatchAll()
	err = ValidateChartRepo(chartRepo, addons)
	assert.NoError(t, err)

	// 无ip domain 无addon
	chartRepo = v1beta1.Repo{}
	addons = addon.Addons{}
	err = ValidateChartRepo(chartRepo, addons)
	assert.NoError(t, err)

	// 无ip domain 有yaml addon
	chartRepo = v1beta1.Repo{}
	addons = addon.Addons{v1beta1.Product{Type: addon.YamlAddon}}
	err = ValidateChartRepo(chartRepo, addons)
	assert.NoError(t, err)

	// 无ip domain 有chart addon
	chartRepo = v1beta1.Repo{}
	addons = addon.Addons{v1beta1.Product{Type: addon.ChartAddon}}
	err = ValidateChartRepo(chartRepo, addons)
	assert.Error(t, err)
}

func TestResolveReachableRepoAddress(t *testing.T) {
	monkey.Patch(checkReachable, func(addr string) bool {
		return true
	})
	defer monkey.UnpatchAll()

	// 无domain
	chartRepo := v1beta1.Repo{
		Domain: "",
		Ip:     "192.168.100.20",
		Port:   "8080",
		Prefix: "chart",
	}
	url, _ := ResolveReachableRepoAddress(chartRepo)
	assert.Contains(t, url, chartRepo.Ip)

	// 无ip
	chartRepo = v1beta1.Repo{
		Domain: "chart.domain",
		Ip:     "",
		Port:   "8080",
		Prefix: "chart",
	}
	url, _ = ResolveReachableRepoAddress(chartRepo)
	assert.Contains(t, url, chartRepo.Domain)
}

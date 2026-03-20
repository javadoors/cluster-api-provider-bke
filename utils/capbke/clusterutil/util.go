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

package clusterutil

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/common"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
)

func AvailableLoadBalancerEndPoint(endPoint confv1beta1.APIEndpoint, nodes bkenode.Nodes) bool {
	if endPoint.IsValid() {
		host := endPoint.Host
		if nodes.Filter(bkenode.FilterOptions{"IP": host}).Length() == 0 {
			return true
		}
	}
	return false
}

// GetIngressConfig 获取bkecluster ingress(ELB) addon配置
func GetIngressConfig(addons []confv1beta1.Product) (string, []string) {
	var lbVIP string
	var lbNodes []string
	var elbAddon *confv1beta1.Product
	for _, addon := range addons {
		if addon.Name == "beyondELB" {
			elbAddon = &addon
			break
		}
	}
	if elbAddon != nil {
		lbVIP = elbAddon.Param["lbVIP"]
		lbNodes = strings.Split(elbAddon.Param["lbNodes"], ",")
		// 去除空字符串
		lbNodes = utils.TrimSpaceSlice(lbNodes)
	}
	return lbVIP, lbNodes
}

func BKEConfigCmKey() client.ObjectKey {
	return client.ObjectKey{
		Namespace: "cluster-system",
		Name:      common.BKEClusterConfigFileName,
	}
}

func GetBKEConfigCMData(ctx context.Context, c client.Client) (map[string]string, error) {
	config := &corev1.ConfigMap{}
	err := c.Get(ctx, BKEConfigCmKey(), config)
	if err != nil {
		return nil, err
	}
	return config.Data, nil
}

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
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/log"
)

// expectedBKENodeName returns the configured node name used for kubelet --hostname-override.
func (ep *EnvPlugin) expectedBKENodeName() string {
	if ep.currenNode.Hostname != "" {
		return ep.currenNode.Hostname
	}
	return utils.HostName()
}

func logOSHostnamePreserved(osHostname, bkeNodeName string) {
	if osHostname != "" && bkeNodeName != "" && osHostname != bkeNodeName {
		log.Infof("OS hostname %q differs from BKE node name %q, kubelet will register node with --hostname-override",
			osHostname, bkeNodeName)
	}
}

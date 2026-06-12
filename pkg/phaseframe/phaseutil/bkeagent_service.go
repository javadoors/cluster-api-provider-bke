/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 ******************************************************************/

package phaseutil

import (
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

// BKEAgentServiceFilePermission is the file permission for rendered bkeagent.service.
const BKEAgentServiceFilePermission = 0644

// RenderBKEAgentServiceContent applies cluster NTPServer and AgentHealthPort to service unit bytes.
// HTTP-downloaded units and /bkeagent.service.tmpl share the same --ntpserver= / --health-port= placeholders.
func RenderBKEAgentServiceContent(bkeCluster *bkev1beta1.BKECluster, raw []byte) []byte {
	ntpServer := ""
	healthPort := ""
	if bkeCluster != nil && bkeCluster.Spec.ClusterConfig != nil {
		ntpServer = bkeCluster.Spec.ClusterConfig.Cluster.NTPServer
		healthPort = bkeCluster.Spec.ClusterConfig.Cluster.AgentHealthPort
	}

	content := strings.ReplaceAll(string(raw), "--ntpserver=", fmt.Sprintf("--ntpserver=%s", ntpServer))
	content = strings.ReplaceAll(content, "--health-port=", fmt.Sprintf("--health-port=%s", healthPort))
	return []byte(content)
}

// WriteRenderedBKEAgentServiceFile renders service content and writes to destPath.
func WriteRenderedBKEAgentServiceFile(bkeCluster *bkev1beta1.BKECluster, destPath string, raw []byte) error {
	if err := os.WriteFile(destPath, RenderBKEAgentServiceContent(bkeCluster, raw), BKEAgentServiceFilePermission); err != nil {
		return errors.Wrap(err, "write bkeagent.service")
	}
	return nil
}

// RenderBKEAgentServiceFile renders /bkeagent.service.tmpl like EnsureBKEAgent.prepareServiceFile.
func RenderBKEAgentServiceFile(bkeCluster *bkev1beta1.BKECluster, servicePath string) error {
	file, err := os.ReadFile("/bkeagent.service.tmpl")
	if err != nil {
		return errors.Wrap(err, "read /bkeagent.service.tmpl")
	}
	return WriteRenderedBKEAgentServiceFile(bkeCluster, servicePath, file)
}

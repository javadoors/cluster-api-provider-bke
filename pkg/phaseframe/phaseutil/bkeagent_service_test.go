/******************************************************************
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 ******************************************************************/

package phaseutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	confv1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/capbke/v1beta1"
)

// RFC 2606 reserved names for unit tests (SCA-safe fixtures).
const (
	testNTPServer       = "ntp.invalid"
	testAgentHealthPort = "9443"
)

func TestRenderBKEAgentServiceContent(t *testing.T) {
	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{
					NTPServer:       testNTPServer,
					AgentHealthPort: testAgentHealthPort,
				},
			},
		},
	}
	raw := []byte("ExecStart=bkeagent --health-port= --ntpserver=\n")
	rendered := string(RenderBKEAgentServiceContent(cluster, raw))
	assert.Contains(t, rendered, "--ntpserver="+testNTPServer)
	assert.Contains(t, rendered, "--health-port="+testAgentHealthPort)
}

func TestRenderBKEAgentServiceFile(t *testing.T) {
	if _, err := os.Stat("/bkeagent.service.tmpl"); err != nil {
		t.Skip("provider template not available in test environment")
	}

	cluster := &bkev1beta1.BKECluster{
		Spec: confv1beta1.BKEClusterSpec{
			ClusterConfig: &confv1beta1.BKEConfig{
				Cluster: confv1beta1.Cluster{
					NTPServer:       testNTPServer,
					AgentHealthPort: testAgentHealthPort,
				},
			},
		},
	}

	out := filepath.Join(t.TempDir(), "bkeagent.service")
	require.NoError(t, RenderBKEAgentServiceFile(cluster, out))
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "--ntpserver="+testNTPServer)
	assert.Contains(t, content, "--health-port="+testAgentHealthPort)
}

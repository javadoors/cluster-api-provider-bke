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

package config

import (
	"flag"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/constant"
)

var (
	MetricsAddr           string
	EnableLeaderElection  bool
	ProbeAddr             string
	ProbeScheme           string // "http" or "https", default "http"
	ProbePort             int    // Port for HTTPS health probe, default 9444
	WebhookCertDir        string
	WebhookPort           int
	WebhookHost           string
	BkeClusterConcurrency int
	BkeMachineConcurrency int
	E2EMode               bool
	EnableInternalUpdate  bool
)

func ConfigurationFlag() {
	flag.BoolVar(&E2EMode, "e2e-mode", false, "Enable e2e mode")
	flag.StringVar(&MetricsAddr, "metrics-bind-address", "0", "The address the metric endpoint binds to. eg. :8080")
	flag.StringVar(&ProbeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&ProbeScheme, "health-probe-scheme", "http", "The scheme for health probe: http or https. Default: http")
	flag.IntVar(&ProbePort, "health-probe-port", 9444, "The port for HTTPS health probe server. Default: 9444 (webhook uses 9443)")
	flag.BoolVar(&EnableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&WebhookCertDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs/",
		"Webhook cert dir, only used when webhook-port is specified.")
	flag.IntVar(&WebhookPort, "webhook-port", 9443, "Webhook Server port")
	flag.StringVar(&WebhookHost, "webhook-host", "", "Webhook Server host")
	flag.IntVar(&BkeClusterConcurrency, "bke-cluster-concurrency", constant.DefaultConcurrency, "Number of BKECluster to process simultaneously")
	flag.IntVar(&BkeMachineConcurrency, "bke-machine-concurrency", constant.DefaultConcurrency, "Number of BKEMachine to process simultaneously")
	flag.BoolVar(&EnableInternalUpdate, "enable-internal-update", false, "Enable internal update")
}

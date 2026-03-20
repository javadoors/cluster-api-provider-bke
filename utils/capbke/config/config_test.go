/*
 *
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 *
 */

package config

import (
	"flag"
	"testing"
)

func TestConfigurationFlag(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flag.CommandLine = fs

	ConfigurationFlag()

	if MetricsAddr != "0" {
		t.Errorf("expected default 0, got %s", MetricsAddr)
	}
	if EnableLeaderElection != false {
		t.Errorf("expected default false, got %v", EnableLeaderElection)
	}
	if ProbeAddr != ":8081" {
		t.Errorf("expected default :8081, got %s", ProbeAddr)
	}
	if ProbeScheme != "http" {
		t.Errorf("expected default http, got %s", ProbeScheme)
	}
	if ProbePort != 9444 {
		t.Errorf("expected default 9444, got %d", ProbePort)
	}
	if WebhookPort != 9443 {
		t.Errorf("expected default 9443, got %d", WebhookPort)
	}
	if E2EMode != false {
		t.Errorf("expected default false, got %v", E2EMode)
	}
	if EnableInternalUpdate != false {
		t.Errorf("expected default false, got %v", EnableInternalUpdate)
	}
}

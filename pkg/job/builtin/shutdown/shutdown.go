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

package shutdown

import (
	"os"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/plugin"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

const Name = "Shutdown"

type ShutDown struct{}

func New() plugin.Plugin {
	return &ShutDown{}
}

func (s ShutDown) Name() string {
	return Name
}

func (s ShutDown) Param() map[string]plugin.PluginParam {
	return map[string]plugin.PluginParam{}
}

func (s ShutDown) Execute(commands []string) ([]string, error) {
	log.Info("shutting down BKEAgent")
	os.Exit(0)
	return nil, nil
}

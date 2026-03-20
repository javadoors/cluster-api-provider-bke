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

package selfupdate

import (
	_ "embed"
	"fmt"
	"os"
	"path"
	"strings"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/plugin"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/version"
)

const (
	Name            = "SelfUpdate"
	DefaultAgentUrl = "http://http.bocloud.k8s:40080/files/bkeagent-latest-linux-{.arch}"
	// RwxRxRx is the permission of the directory
	RwxRxRx = 0755
)

var restartScript = path.Join(utils.AgentScripts, "update.sh")

//go:embed update.sh
var updateScript string

type UpdatePlugin struct {
	exec exec.Executor
}

func New(exec exec.Executor) plugin.Plugin {
	return &UpdatePlugin{
		exec: exec,
	}
}

func (u UpdatePlugin) Name() string {
	return Name
}

func (u UpdatePlugin) Param() map[string]plugin.PluginParam {
	return map[string]plugin.PluginParam{
		"agentUrl": {
			Key:         "agentUrl",
			Value:       DefaultAgentUrl,
			Required:    false,
			Default:     DefaultAgentUrl,
			Description: "Agent download url used to replace existing BKEAgent binary",
		},
	}
}

func (u UpdatePlugin) Execute(commands []string) ([]string, error) {
	agentName := "bkeagent"
	binPath := path.Join(utils.AgentBin, agentName)

	if !u.NeedUpdate(binPath) {
		log.Infof("The agent is up to date, skip!")
		return nil, nil
	}

	if !utils.Exists(utils.AgentScripts) {
		if err := os.MkdirAll(utils.AgentScripts, RwxRxRx); err != nil {
			log.Errorf("Failed to create dir %s, err: %v", utils.AgentScripts, err)
			return nil, err
		}
	}

	if !utils.Exists(restartScript) {
		if err := os.WriteFile(restartScript, []byte(updateScript), RwxRxRx); err != nil {
			log.Errorf("Failed to create file %s, err:", restartScript, err)
			return nil, err
		}
	}

	// 执行update.sh
	if err := u.exec.ExecuteCommand("/bin/sh", "-c", fmt.Sprintf("nohup %s %s >/dev/null 2>&1 &", restartScript, binPath)); err != nil {
		return nil, err
	}
	return nil, nil
}

func (u UpdatePlugin) NeedUpdate(binPath string) bool {
	// 获取当前编译版本
	gitCommitID := version.GitCommitID
	downloadGitCommitID := ""
	if utils.Exists(binPath) {
		out, err := u.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", fmt.Sprintf("chmod +x %s && %s -v", binPath, binPath))
		if err == nil && out != "" {
			// 获取out第一行
			downloadGitCommitID = strings.Split(out, "\n")[0]
		}
	}

	return gitCommitID != downloadGitCommitID
}

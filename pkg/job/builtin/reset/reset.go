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

package reset

import (
	"net"
	"path"
	"strings"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	bkenet "gopkg.openfuyao.cn/cluster-api-provider-bke/common/utils/net"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/plugin"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
)

const (
	Name = "Reset"
)

type ResetPlugin struct {
}

func New() plugin.Plugin {
	return &ResetPlugin{}
}

func (r ResetPlugin) Name() string {
	return Name
}

func (r ResetPlugin) Param() map[string]plugin.PluginParam {
	return map[string]plugin.PluginParam{
		"bkeConfig": {
			Key:         "bkeConfig",
			Value:       "ns:name",
			Required:    false,
			Default:     "",
			Description: "bke config",
		},
		"scope": {
			Key:         "scope",
			Value:       "cert,manifests,containerd-cfg,container,kubelet,containerRuntime,source,extra",
			Required:    false,
			Default:     "cert,manifests,container,kubelet,containerRuntime,source,extra",
			Description: "reset scope(cert,)",
		},
		"extra": {
			Key:         "extra",
			Value:       "extra",
			Required:    false,
			Default:     "",
			Description: "extra file/dir/ipaddr to clean,split by ',' ",
		},
	}
}

func (r ResetPlugin) Execute(commands []string) ([]string, error) {
	commandsMap, err := plugin.ParseCommands(r, commands)
	if err != nil {
		return nil, err
	}

	var cfg *bkev1beta1.BKEConfig
	var nodesData bkenode.Nodes
	if v, ok := commandsMap["bkeConfig"]; ok && v != "" {
		cfg, err = plugin.GetBkeConfig(v)
		if err != nil {
			log.Errorf("get bke config failed: %v", err)
			return nil, err
		}
		nodesData, err = plugin.GetNodesData(v)
		if err != nil {
			log.Errorf("get bke node failed: %v", err)
			return nil, err
		}
	}
	if cfg == nil {
		log.Errorf("bke config is required but not provided")
		return nil, err
	}

	cfg.CustomExtra["allInOne"] = "false"

	nodes := bkenode.Nodes(nodesData)
	cnode, err := nodes.CurrentNode()
	if err != nil {
		log.Warnf("get current node failed: %v", err)
	}

	if v, ok := cfg.CustomExtra["host"]; ok && v != "" {
		if cnode.IP != "" && cnode.IP == v {
			cfg.CustomExtra["allInOne"] = "true"
		}
		inter, err := bkenet.GetInterfaceFromIp(v)
		if err == nil && inter != "" {
			cfg.CustomExtra["allInOne"] = "true"
		}
	}
	scopes := strings.Split(commandsMap["scope"], ",")

	if err = r.executeCleanPhases(cfg, scopes, commandsMap["extra"]); err != nil {
		return nil, err
	}

	return nil, nil
}

// executeCleanPhases 执行清理阶段
func (r ResetPlugin) executeCleanPhases(cfg *bkev1beta1.BKEConfig, scopes []string, extraArgs string) error {
	phases := DefaultCleanPhases()
	for i := range phases {
		phase := &phases[i]
		if !utils.ContainsString(scopes, phase.Name) {
			continue
		}

		if phase.Name == "extra" {
			if err := r.processExtraPhase(phase, extraArgs); err != nil {
				return err
			}
		}

		if err := phase.Clean(cfg); err != nil {
			return err
		}
	}
	return nil
}

// processExtraPhase 处理 extra phase 的清理逻辑
func (r ResetPlugin) processExtraPhase(phase *CleanPhase, extraArgs string) error {
	args := strings.Split(extraArgs, ",")
	for _, arg := range args {
		if arg == "" {
			continue
		}

		if !path.IsAbs(arg) {
			if ip := net.ParseIP(arg); ip != nil {
				phase.AddIPToClean(arg)
			} else {
				log.Warnf("not a valid ip addr, skip remove", arg)
			}
			continue
		}

		if !utils.Exists(arg) {
			log.Warnf("extra file/dir %s not exists, skip remove", arg)
			continue
		}

		if utils.IsDir(arg) {
			phase.AddDirToClean(arg)
		} else {
			phase.AddFileToClean(arg)
		}
	}
	return nil
}

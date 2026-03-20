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
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/exec"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/plugin"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
	netutil "gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/net"
)

const Name = "K8sEnvInit"

const CommandPrefix = "/bin/sh"

const (
	defaultInit   = "true"
	defaultCheck  = "true"
	defaultSudo   = "true"
	defaultBackup = "true"
	defaultScope  = "kernel,firewall,selinux,swap,time,hosts,runtime,image,node,ports"
)

// check and init file paths
const (
	DefaultIpMode = "ipv4"

	procSysPath     = "/proc/sys/"
	rcLoaclFilePath = "/etc/rc.d/rc.local"

	InitKernelConfPath          = "/etc/sysctl.d/k8s.conf"
	InitSwapConfPath            = "/etc/sysctl.d/k8s-swap.conf"
	InitSelinuxConfPath         = "/etc/selinux/config"
	InitHostConfPath            = "/etc/hosts"
	InitNetWorkManagerPath      = "/etc/NetworkManager/NetworkManager.conf"
	InitDNSConfPath             = "/etc/resolv.conf"
	InitIpvsSysModuleFilePath   = "/etc/sysconfig/modules/ip_vs.modules"
	InitFileLimitConfPath       = "/etc/security/limits.conf"
	InitUbuntuSysModuleFilePath = "/etc/modules"

	CheckSelinuxConfPath         = InitSelinuxConfPath
	CheckSwapConfPath            = "/proc/meminfo"
	CheckHostConfPath            = InitHostConfPath
	CheckDNSConfPath             = InitDNSConfPath
	CheckNetWorkManagerPath      = InitNetWorkManagerPath
	CheckIpvsSysModuleFilePath   = InitIpvsSysModuleFilePath
	CheckFileLimitConfPath       = InitFileLimitConfPath
	CheckUbuntuSysModuleFilePath = InitUbuntuSysModuleFilePath

	nfConntrackIpv4Block = `

/sbin/modinfo -F filename nf_conntrack_ipv4 > /dev/null 2>&1
if [ $? -eq 0 ]; then
    /sbin/modprobe nf_conntrack_ipv4
fi
`
	nfConntrackBlock = `

/sbin/modinfo -F filename nf_conntrack > /dev/null 2>&1
if [ $? -eq 0 ]; then
	/sbin/modprobe nf_conntrack
fi
`
)

// Regex
const (
	SelinuxRegex    = `^SELINUX=.*`
	SwapRegex       = `SwapTotal(.*)0(.*)`
	SoftLimitsRegex = "(?:.*)(\\* soft nofile)(?:.*)"
	HardLimitsRegex = "(?:.*)(\\* hard nofile)(?:.*)"
)

// kernel param
var (
	sysModule   = []string{"ip_vs", "ip_vs_wrr", "ip_vs_rr", "ip_vs_sh", "fuse", "rbd", "br_netfilter"}
	kernelParam = map[string]map[string]string{
		"ipv4": {
			"net.ipv4.conf.all.rp_filter":     "0",
			"net.ipv4.conf.default.rp_filter": "0",
		},
		"ipv6": {
			"net.bridge.bridge-nf-call-ip6tables": "1",
			"net.bridge.bridge-nf-call-iptables":  "1",
			"net.ipv6.conf.all.forwarding":        "1",
			"net.ipv6.conf.default.forwarding":    "1",
			"net.ipv4.conf.all.rp_filter":         "0",
			"net.ipv6.conf.all.disable_ipv6":      "0",
			"net.ipv6.conf.default.disable_ipv6":  "0",
			"net.ipv6.conf.lo.disable_ipv6":       "0",
		},
	}
	defaultKernelParam = map[string]string{
		"net.ipv4.ip_forward":           "1",
		"vm.max_map_count":              "262144",
		"fs.inotify.max_user_watches":   "1000000",
		"fs.inotify.max_user_instances": "1000000",
		// for containerd and centos 7 TODO ingore for centos 8
		//"fs.may_detach_mounts": "1",
	}
	// 127.0.0.1   localhost localhost.localdomain localhost4 localhost4.localdomain4
	// ::1         localhost localhost.localdomain localhost6 localhost6.localdomain6

	execKernelParam = map[string]string{}

	defaultInsecureRegistries = []string{
		"dcr.io:5000",
		"abcsys.cn:5000",
		"abcsys.cn:40443",
		"registry01.com:5000",
		"registry.com:5000",
		"deploy.bocloud.k8s:40443",
		"docker.io",
		"registry.k8s.io",
		"k8s.gcr.io",
		"ghcr.io",
		"quay.io",
		"gcr.io",
		"cr.openfuyao.cn",
		"hub.oepkgs.net",
	}
)

type EnvPlugin struct {
	exec      exec.Executor
	k8sClient client.Client

	bkeConfig   *bkev1beta1.BKEConfig
	bkeConfigNS string
	currenNode  bkenode.Node
	nodes       bkenode.Nodes

	sudo   string
	scope  string
	backup string

	extraHosts   string
	clusterHosts []string
	hostPort     []string

	machine *Machine
}

func init() {
	for k, v := range kernelParam[DefaultIpMode] {
		execKernelParam[k] = v
	}
	for k, v := range defaultKernelParam {
		execKernelParam[k] = v
	}
	face, err := netutil.GetV4Interface()
	if err != nil {
		log.Error(err, "Get ipv4 default interface failed")
	} else {
		key := fmt.Sprintf("net.ipv4.conf.%s.rp_filter", face)
		execKernelParam[key] = "0"
	}
}

func New(exec exec.Executor, cfg *bkev1beta1.BKEConfig) plugin.Plugin {
	return &EnvPlugin{
		exec:      exec,
		bkeConfig: cfg,
		machine:   NewMachine(),
	}
}

func (ep *EnvPlugin) Name() string {
	return Name
}

func (ep *EnvPlugin) Param() map[string]plugin.PluginParam {
	return map[string]plugin.PluginParam{
		"check":      {Key: "check", Value: "true,false", Required: false, Default: defaultCheck, Description: "Check whether the machine conforms to the k8s operating environment"},
		"init":       {Key: "init", Value: "true,false", Required: false, Default: defaultInit, Description: "init k8s environment,if enable init,check will be auto run after init"},
		"sudo":       {Key: "sudo", Value: "true,false", Required: false, Default: defaultSudo, Description: "use sudo to execute commands"},
		"scope":      {Key: "scope", Value: "kernel,firewall,selinux,swap,time,hosts,ports,image,node,httpRepo,iptables", Required: false, Default: defaultScope, Description: "scope of the k8s environment to check or init"},
		"backup":     {Key: "backup", Value: "true,false", Required: false, Default: defaultBackup, Description: "make a backup before modifying files"},
		"extraHosts": {Key: "extraHosts", Value: "hostname1:ip1,hostname2:ip2", Required: false, Default: "", Description: "use given host info to set node host file,required when scope contains hosts"},
		"hostPort":   {Key: "hostPort", Value: "port1,port2", Required: false, Default: "10259,10257,10250,2379,2380,2381,10248", Description: "use given host info to set node host file,required when scope contains hosts"},
		"bkeConfig":  {Key: "bkeConfig", Value: "", Required: false, Default: "", Description: "example ns:name"},
	}
}

// Execute Install and start Containerd
// example :
// 1.init k8s environment and not check for follows scope bridge,firewall.
//
//	if init need use shell command add sudo prefix.Do not back up before modifying files
//
// ["K8sEnvInit", "init=true", "check=false", "sudo=true", "scope=bridge,firewall", "backup=false"]
// 2.use all default value to init k8s environment, example ["k8sEnvInit"]
func (ep *EnvPlugin) Execute(commands []string) ([]string, error) {
	envParamMap, err := plugin.ParseCommands(ep, commands)
	if err != nil {
		return nil, err
	}
	ep.sudo = envParamMap["sudo"]
	ep.scope = envParamMap["scope"]
	ep.backup = envParamMap["backup"]
	ep.extraHosts = envParamMap["extraHosts"]
	ep.hostPort = strings.Split(envParamMap["hostPort"], ",")
	ep.machine = NewMachine()

	if envParamMap["bkeConfig"] != "" {
		ep.bkeConfigNS = envParamMap["bkeConfig"]
		ep.bkeConfig = &bkev1beta1.BKEConfig{}
		cfg, err := plugin.GetBkeConfig(envParamMap["bkeConfig"])
		if err != nil {
			return nil, err
		}
		ep.bkeConfig = cfg
		clusterData, err := plugin.GetClusterData(envParamMap["bkeConfig"])
		if err != nil {
			return nil, err
		}
		ep.nodes = bkenode.Nodes(clusterData.Nodes)
		cNode, err := ep.nodes.CurrentNode()
		if err != nil {
			return nil, errors.Wrap(err, "get current node failed")
		}
		ep.currenNode = cNode
	}

	if envParamMap["init"] == "true" {
		if err := ep.initK8sEnv(); err != nil {
			return nil, err
		}
	}

	if envParamMap["check"] == "true" || envParamMap["init"] == "true" {
		if err := ep.checkK8sEnv(); err != nil {
			return nil, err
		}
	}
	return nil, err
}

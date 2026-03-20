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
	"bytes"
	_ "embed"
	"fmt"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/pkg/errors"
	kerrors "k8s.io/apimachinery/pkg/util/errors"

	bkev1beta1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/bkecommon/v1beta1"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/imagehelper"
	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkevalidte "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/validation"
	bkesource "gopkg.openfuyao.cn/cluster-api-provider-bke/common/source"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/containerd"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/docker"
	edocker "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/executor/docker"
	containerdPlugin "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/containerruntime/containerd"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/containerruntime/cridocker"
	dockerPlugin "gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/containerruntime/docker"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/job/builtin/downloader"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/httprepo"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/initsystem"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/runtime"
)

//go:embed tmpl/system.modules
var systemModules string

const MaxUint16 = 65535

// processSimpleInitScope processes simple init scopes that only call one function
func (ep *EnvPlugin) processSimpleInitScope(logMsg string, initFunc func() error) error {
	log.Infof(logMsg)
	return initFunc()
}

// processInitScope processes a single init scope and returns error and kernel change flag
func (ep *EnvPlugin) processInitScope(scope string) (error, bool) {
	switch scope {
	case "kernel":
		log.Infof("Start to init kernel parameters")
		if err := ep.initKernelParam(); err != nil {
			log.Warnf("(ignore)init kernel parameters failed: %v", err)
			return nil, false
		}
		return nil, true
	case "swap":
		if err := ep.processSimpleInitScope("Start to disable swap", ep.initSwap); err != nil {
			return err, false
		}
		return nil, true
	case "firewall":
		return ep.processSimpleInitScope("Start to disable firewall", ep.initFirewall), false
	case "selinux":
		return ep.processSimpleInitScope("Start to disable selinux", ep.initSelinux), false
	case "time":
		return ep.processSimpleInitScope("Start to sync time cron job", ep.initTime), false
	case "hosts":
		return ep.processSimpleInitScope("Start to write hosts file", ep.initHost), false
	case "image":
		return ep.processSimpleInitScope("Start to pull container images", ep.initImage), false
	case "runtime":
		return ep.processSimpleInitScope("Start to init container runtime", ep.initRuntime), false
	case "dns":
		return ep.processSimpleInitScope("Start to init dns", ep.initDNS), false
	case "httpRepo":
		return ep.processSimpleInitScope("Start to init http repo", ep.initHttpRepo), false
	case "iptables":
		return ep.processSimpleInitScope("Start to init iptables", ep.initIptables), false
	case "registry":
		return ep.processSimpleInitScope("Start to init registry", ep.initRegistry), false
	case "extra":
		log.Infof("extra scope already deprecated")
		out1, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "umask 0022")
		if err != nil {
			log.Warnf("umask 0022 failed, err: %s, output: %s", err, out1)
		}
		return nil, false
	default:
		log.Warnf("Unknown init scope: %s, skipping", scope)
		return nil, false
	}
}

// initK8sEnv init k8s environment
func (ep *EnvPlugin) initK8sEnv() error {
	var initErrs []error
	kernelChangeFlag := false

	for _, s := range strings.Split(ep.scope, ",") {
		err, kernelChanged := ep.processInitScope(s)
		if err != nil {
			initErrs = append(initErrs, err)
		}
		if kernelChanged {
			kernelChangeFlag = true
		}
	}

	if len(initErrs) > 0 {
		return kerrors.NewAggregate(initErrs)
	}

	if kernelChangeFlag {
		// try load InitKernelConf and InitSwapConf
		_ = ep.exec.ExecuteCommand("/bin/sh", "-c", "sudo sysctl -p "+InitKernelConfPath)
		_ = ep.exec.ExecuteCommand("/bin/sh", "-c", "sudo sysctl -p "+InitSwapConfPath)
	}
	return nil
}

// setupUlimit 设置文件描述符限制
func (ep *EnvPlugin) setupUlimit() {
	output, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "ulimit -n")
	if err != nil {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil || n > MaxUint16 {
		return
	}

	log.Infof("ulimit -n is %d, try set it to 65536", n)
	if !utils.Exists(InitFileLimitConfPath) {
		if err := os.WriteFile(InitFileLimitConfPath, []byte("* hard nofile 65536\n* soft nofile 65536\n"), RwRR); err != nil {
			log.Warnf("write file %s failed: %v", InitFileLimitConfPath, err)
		}
		return
	}

	if found, err := catAndSearch(InitFileLimitConfPath, "", HardLimitsRegex); err == nil && !found {
		log.Infof("not found '* hard nofile' config in %s, add it", InitFileLimitConfPath)
		ep.exec.ExecuteCommand("/bin/sh", "-c", "echo '* hard nofile 65536' >> "+InitFileLimitConfPath)
	} else {
		log.Infof("found '* hard nofile' config in %s, replace it", InitFileLimitConfPath)
		_ = catAndReplace(InitFileLimitConfPath, "", "* hard nofile 65536", HardLimitsRegex)
	}

	if found, err := catAndSearch(InitFileLimitConfPath, "", SoftLimitsRegex); err == nil && !found {
		log.Infof("not found '* soft nofile' config in %s, add it", InitFileLimitConfPath)
		ep.exec.ExecuteCommand("/bin/sh", "-c", "echo '* soft nofile 65536' >> "+InitFileLimitConfPath)
	} else {
		log.Infof("found '* soft nofile' config in %s, replace it", InitFileLimitConfPath)
		_ = catAndReplace(InitFileLimitConfPath, "", "* soft nofile 65536", SoftLimitsRegex)
	}
}

// setupCentos7DetachMounts 为 CentOS 7 设置 fs.may_detach_mounts 参数
func (ep *EnvPlugin) setupCentos7DetachMounts() {
	if ep.machine.platform != "centos" {
		return
	}
	if !strings.HasPrefix(ep.machine.version, "7") || ep.bkeConfig.Cluster.ContainerRuntime.CRI != bkeinit.CRIContainerd {
		return
	}

	execKernelParam["fs.may_detach_mounts"] = "1"
	if !utils.Exists("/proc/sys/fs/may_detach_mounts") {
		file, err := os.OpenFile("/proc/sys/fs/may_detach_mounts", os.O_CREATE, RwxRxRx)
		if err != nil {
			log.Errorf("Open file /proc/sys/fs/may_detach_mounts failed, err: %v", err)
		}
		_, err = file.WriteString("1")
		if err != nil {
			log.Errorf("Write file /proc/sys/fs/may_detach_mounts failed, err: %v", err)
		}
		err = file.Close()
		if err != nil {
			log.Errorf("Close file /proc/sys/fs/may_detach_mounts failed, err: %v", err)
		}
	}

	found, err := catAndSearch("/etc/sysctl.conf", "", "fs.may_detach_mounts=1")
	if err != nil {
		log.Warnf("cat /etc/sysctl.conf failed, err: %v", err)
		return
	}
	if !found {
		_ = ep.exec.ExecuteCommand("/bin/sh", "-c", "echo fs.may_detach_mounts=1 >> /etc/sysctl.conf")
		_ = ep.exec.ExecuteCommand("/bin/sh", "-c", "sysctl -p")
	}
}

// setupIPVSConfig 配置 IPVS 相关的内核参数和模块
func (ep *EnvPlugin) setupIPVSConfig() {
	if ep.bkeConfig.CustomExtra["proxyMode"] != "ipvs" {
		return
	}

	if utils.Exists("/proc/sys/net/ipv4/vs/conntrack") {
		execKernelParam["net.ipv4.vs.conntrack"] = "1"
	}

	if ep.machine.hostOS != "linux" {
		return
	}

	major := ep.machine.kernel[0:1]
	log.Debugf("check kernel version: %s, major: %s", ep.machine.kernel, major)
	majorInt, err := strconv.Atoi(major)
	if err != nil {
		log.Warnf("Failed to configure kubeproxy ipvs module, get kernel version failed, err: %v", err)
		return
	}

	switch {
	case majorInt > three:
		sysModule = append(sysModule, "nf_conntrack")
		systemModules = systemModules + nfConntrackBlock
	case majorInt == three:
		log.Infof("Enable nf_conntrack_ipv4 module")
		sysModule = append(sysModule, "nf_conntrack_ipv4")
		systemModules = systemModules + nfConntrackIpv4Block
	default:
		sysModule = append(sysModule, "nf_conntrack", "nf_conntrack_ipv4")
	}
}

// writeKernelParams 将内核参数写入文件
func (ep *EnvPlugin) writeKernelParams(f *os.File) []error {
	var errs []error
	for k, v := range execKernelParam {
		if _, err := f.WriteString(fmt.Sprintf("%s=%s\n", k, v)); err != nil {
			errs = append(errs, errors.Wrapf(err, "write kernel param %s=%s to %s failed", k, v, InitKernelConfPath))
			continue
		}
		log.Infof("write kernel param %s=%s to %s success", k, v, InitKernelConfPath)
	}
	return errs
}

// loadSysModules 加载系统模块
func (ep *EnvPlugin) loadSysModules() []error {
	var errs []error
	for _, m := range sysModule {
		if out, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", fmt.Sprintf("modprobe %s", m)); err != nil {
			errInfo := errors.Errorf("load sys module %q failed, err: %v, output: %s", m, err, out)
			errs = append(errs, errInfo)
			continue
		}
		log.Infof("load sys module %q success", m)
	}
	return errs
}

// setupUbuntuModules 在 Ubuntu 中添加模块到 /etc/modules
func (ep *EnvPlugin) setupUbuntuModules() []error {
	if ep.machine.platform != "ubuntu" {
		return nil
	}

	var errs []error
	for _, m := range sysModule {
		found, err := catAndSearch(InitUbuntuSysModuleFilePath, m, "")
		if err != nil {
			errs = append(errs, errors.Errorf("cat %s failed, err: %v", InitUbuntuSysModuleFilePath, err))
			continue
		}
		if !found {
			combinedOutput, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", fmt.Sprintf("echo %q >> %s", m, InitUbuntuSysModuleFilePath))
			if err != nil {
				errs = append(errs, errors.Errorf("echo %q >> %s failed, err: %v, output: %s", m, InitUbuntuSysModuleFilePath, err, combinedOutput))
			}
		}
	}
	return errs
}

// setupCentosKylinModules 在 CentOS 和 Kylin 中创建模块文件
func (ep *EnvPlugin) setupCentosKylinModules() []error {
	if ep.machine.platform != "centos" && ep.machine.platform != "kylin" {
		return nil
	}

	var errs []error
	log.Infof("create sys module file %s", InitIpvsSysModuleFilePath)
	if err := os.MkdirAll(filepath.Dir(InitIpvsSysModuleFilePath), RwxRxRx); err != nil {
		errs = append(errs, errors.Errorf("create sys module file %s failed, err: %v", InitIpvsSysModuleFilePath, err))
	}
	if err := os.WriteFile(InitIpvsSysModuleFilePath, []byte(systemModules), RwxRxRx); err != nil {
		errs = append(errs, errors.Errorf("write sys module file %s failed, err: %v", InitIpvsSysModuleFilePath, err))
	}
	return errs
}

// setupKylinRcLocal 在 Kylin 中配置 rc.local
func (ep *EnvPlugin) setupKylinRcLocal() []error {
	if ep.machine.platform != "kylin" {
		return nil
	}

	var errs []error
	sources := "source /etc/sysconfig/modules/ip_vs.modules"
	if !utils.Exists(rcLoaclFilePath) {
		if err := os.WriteFile(rcLoaclFilePath, []byte(sources), RwxRxRx); err != nil {
			return []error{err}
		}
		return nil
	}

	output, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", fmt.Sprintf("sudo echo %q >> %s", sources, rcLoaclFilePath))
	if err != nil {
		errs = append(errs, errors.Errorf("failed to add %s to %s,err: %v,out: %s", sources, rcLoaclFilePath, err, output))
	}
	output, err = ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", fmt.Sprintf("sudo chmod +x /etc/rc.d/rc.local"))
	if err != nil {
		errs = append(errs, errors.Errorf("failed to chmod +x %s,err: %v,out: %s", rcLoaclFilePath, err, output))
	}
	return errs
}

// initBridge enable network bridge param bridge-nf-call-iptables || bridge-nf-call-ip6tables
func (ep *EnvPlugin) initKernelParam() error {

	// todo support ipv6

	f, err := os.OpenFile(InitKernelConfPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, RwRR)
	if err != nil {
		return errors.Wrap(err, "Open file failed when init kernel net bridge")
	}
	defer f.Close()

	ep.setupUlimit()

	if ep.bkeConfig != nil {
		ep.setupCentos7DetachMounts()
		ep.setupIPVSConfig()
	}

	var errs []error
	errs = append(errs, ep.writeKernelParams(f)...)
	errs = append(errs, ep.loadSysModules()...)
	errs = append(errs, ep.setupUbuntuModules()...)
	errs = append(errs, ep.setupCentosKylinModules()...)
	errs = append(errs, ep.setupKylinRcLocal()...)

	return kerrors.NewAggregate(errs)
}

// initFirewall disable firewalld
func (ep *EnvPlugin) initFirewall() error {

	initSystem, err := initsystem.GetInitSystem()
	if err != nil {
		log.Errorf("Get init system failed: %v", err)
		return err
	}

	if initSystem.ServiceExists("firewalld") {
		log.Info("stop firewalld")
		if err := initSystem.ServiceStop("firewalld"); err != nil {
			log.Errorf("Stop firewalld failed: %v", err)
			return err
		}
		log.Infof("disable firewalld")
		if err := initSystem.ServiceDisable("firewalld"); err != nil {
			log.Warnf("Disable firewalld failed: %v", err)
		}
	}

	if initSystem.ServiceExists("ufw") {

		if initSystem.ServiceIsActive("ufw") {
			log.Info("stop ufw")
			if err := initSystem.ServiceStop("ufw"); err != nil {
				log.Errorf("Stop ufw failed: %v", err)
				return err
			}
		}

		log.Infof("disable ufw")
		if err := initSystem.ServiceDisable("ufw"); err != nil {
			log.Warnf("Disable ufw failed: %v", err)
		}

	}
	return nil
}

// initSelinux disable selinux
func (ep *EnvPlugin) initSelinux() error {
	// skip ubuntu and openEuler
	if ep.machine.platform == utils.UbuntuOS || ep.machine.platform == utils.OpenEulerOS {
		return nil
	}

	// next way need reboot ,so use  "/bin/sh -c setenforce 0" try to close selinux
	// if setenforce 0 execute failed, return
	// todo if exec "/bin/sh -c setenforce 0" failed, need to reboot?
	if out, err := ep.exec.ExecuteCommandWithOutput("/bin/sh", "-c", "sudo setenforce 0"); err != nil {
		// todo this command will return other output, need to handle
		log.Warnf("setenforce 0 failed,err: %s, output: %s", err, out)
	}

	if err := catAndReplace(InitSelinuxConfPath, "", "SELINUX=disabled", SelinuxRegex); err != nil {
		return errors.Wrap(err, "Disable selinux failed")
	}
	return nil
}

// initSwap disable swap
func (ep *EnvPlugin) initSwap() error {
	if err := ep.bakFile(InitSwapConfPath); err != nil {
		return err
	}

	err := ep.exec.ExecuteCommand("/bin/sh", "-c", "sudo sed -ri 's/.*swap.*/#&/' /etc/fstab")
	if err != nil {
		return err
	}

	if out, err := ep.exec.ExecuteCommandWithOutput("/bin/sh", "-c", "sudo swapoff -a"); err != nil {
		log.Warnf("swapoff -a failed,err: %s, output: %s", err, out)
		return errors.Wrapf(err, "swapoff -a failed, output: %s", out)
	}

	f, err := os.OpenFile(InitSwapConfPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, RwRR)
	if err != nil {
		log.Warnf("Open file failed when init swap")
		return errors.Wrap(err, "Open file failed when init kernel vm swappiness")
	}
	defer f.Close()
	if _, err := f.WriteString("vm.swappiness=0\n"); err != nil {
		log.Warnf("write kernel param vm.swappiness=0 to %s failed", InitSwapConfPath)
		return errors.Wrap(err, "Write file failed when init kernel vm swappiness")
	}
	return nil
}

// initTime sync time
func (ep *EnvPlugin) initTime() error {
	server, err := getNTPServer(ep.bkeConfig)
	if err != nil {
		log.Warnf("get ntp server failed, err: %s", err)
		return err
	}
	if server == "" {
		log.Warnf("no ntp server configured, skip")
		return nil
	}

	//设置时区软链接
	if out, err := ep.exec.ExecuteCommandWithOutput("/bin/sh", "-c", "sudo ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime"); err != nil {
		log.Errorf("set time zone failed, err: %s, output: %s", err, out)
		return errors.Wrapf(err, "set time zone failed, output: %s", out)
	}
	// 设置time包的时区
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		time.Local = loc
	}
	return nil
}

// buildClusterHosts builds cluster hosts from bkeConfig
func (ep *EnvPlugin) buildClusterHosts(bkeNodeName string) {
	if ep.bkeConfig == nil {
		return
	}
	for _, n := range ep.nodes {
		tmpHostName := n.Hostname
		if tmpHostName == "" {
			tmpHostName = bkeNodeName
		}
		hostInfo := fmt.Sprintf("%s:%s", tmpHostName, n.IP)
		ep.clusterHosts = append(ep.clusterHosts, hostInfo)
	}

	if ep.bkeConfig.Cluster.ImageRepo.Domain != "" && ep.bkeConfig.Cluster.ImageRepo.Ip != "" {
		imageHostInfo := fmt.Sprintf("%s:%s", ep.bkeConfig.Cluster.ImageRepo.Domain, ep.bkeConfig.Cluster.ImageRepo.Ip)
		ep.clusterHosts = append(ep.clusterHosts, imageHostInfo)
	}
	if ep.bkeConfig.Cluster.HTTPRepo.Domain != "" && ep.bkeConfig.Cluster.HTTPRepo.Ip != "" {
		yumHostInfo := fmt.Sprintf("%s:%s", ep.bkeConfig.Cluster.HTTPRepo.Domain, ep.bkeConfig.Cluster.HTTPRepo.Ip)
		ep.clusterHosts = append(ep.clusterHosts, yumHostInfo)
	}
}

// initHost set host
func (ep *EnvPlugin) initHost() error {
	defer func() {
		ep.clusterHosts = []string{}
	}()

	// 设置hostanme
	hostname, err := os.Hostname()
	if err != nil {
		return errors.Wrap(err, "Get hostname failed when init hostanme")
	}

	bkeNodeName := utils.HostName()
	if hostname != bkeNodeName {
		ep.trySetHostName(bkeNodeName)
	}

	h, err := NewHostsFile(InitHostConfPath)
	if err != nil {
		return errors.Wrapf(err, "init hosts file failed, get hosts file %s failed", InitHostConfPath)
	}
	// todo default add
	// 127.0.0.1   localhost localhost.localdomain localhost4 localhost4.localdomain4
	// ::1         localhost localhost.localdomain localhost6 localhost6.localdomain6

	extraHosts := strings.Split(ep.extraHosts, ",")
	ep.buildClusterHosts(bkeNodeName)

	extraHosts = append(extraHosts, ep.clusterHosts...)
	if len(extraHosts) == 0 {
		return nil
	}
	for _, host := range extraHosts {
		if host == "" {
			continue
		}
		hostWithIP := strings.Split(host, ":")
		if len(hostWithIP) != two {
			return errors.Errorf("init hosts failed,hosts format error, host: %s", host)
		}
		// default ip type use "ip"
		addr, err := net.ResolveIPAddr("ip", hostWithIP[1])
		if err != nil {
			return errors.Wrapf(err, "init hosts failed,resolve ip address %s failed,not a valid ip address", hostWithIP[1])
		}
		h.Set(addr, hostWithIP[0])
		log.Infof("add host %s to hosts file", host)
	}

	if err := h.WriteHostsFileTo(InitHostConfPath); err != nil {
		log.Warnf("Write hosts file failed when init host, err: %s", err)
		return errors.Wrapf(err, "init hosts failed,write hosts info to hosts file %s ailed", InitHostConfPath)
	}

	return nil
}

func (ep *EnvPlugin) trySetHostName(bkeNodeName string) {
	// 方法1 成功后直接返回
	if out, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "sudo hostnamectl set-hostname "+bkeNodeName); err != nil {
		log.Warnf("use hostnamectl command set hostname failed, err: %s, output: %s", err, out)
	} else {
		return
	}
	// 方法2
	if out, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "sudo hostname "+bkeNodeName); err != nil {
		log.Warnf("use hostname command set hostname failed, err: %s, output: %s", err, out)
	}
	// 方法3
	if found, err := catAndSearch("/etc/sysconfig/network", "HOSTNAME=", ""); err != nil {
		log.Warnf("search hostname config in /etc/sysconfig/network failed, err: %s", err)
	} else {
		if !found {
			if out, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "sudo echo HOSTNAME="+bkeNodeName+" >> /etc/sysconfig/network"); err != nil {
				log.Warnf("add hostname config to /etc/sysconfig/network failed, err: %s, output: %s", err, out)
			}
		} else {
			if out, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "sudo sed -i 's/HOSTNAME=.*/HOSTNAME="+bkeNodeName+"/g' /etc/sysconfig/network"); err != nil {
				log.Warnf("modify hostname config in /etc/sysconfig/network failed, err: %s, output: %s", err, out)
			}
		}
	}
	// 方法4
	if out, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "sudo echo "+bkeNodeName+" > /proc/sys/kernel/hostname"); err != nil {
		log.Warnf("set hostname to /proc/sys/kernel/hostname failed, err: %s, output: %s", err, out)
	}
	// 方法5
	if out, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "sudo echo "+bkeNodeName+" > /etc/hostname"); err != nil {
		log.Warnf("set hostname to /etc/hostname failed, err: %s, output: %s", err, out)
	}
}

// initImage pull image
func (ep *EnvPlugin) initImage() error {
	switch runtime.DetectRuntime() {
	case runtime.ContainerRuntimeDocker:
		dockerClient, err := docker.NewDockerClient()
		if err != nil {
			return errors.Wrap(err, "get docker client failed")
		}
		for _, image := range ep.exportImageList() {
			if err := dockerClient.EnsureImageExists(docker.ImageRef{Image: image}); err != nil {
				return errors.Wrapf(err, "pull image %s failed", image)
			}
		}
	case runtime.ContainerRuntimeContainerd:
		containerdClient, err := containerd.NewContainedClient()
		if err != nil {
			return errors.Wrap(err, "failed to create containerd client")
		}
		for _, image := range ep.exportImageList() {
			if err := containerdClient.EnsureImageExists(containerd.ImageRef{Image: image}); err != nil {
				return errors.Wrapf(err, "failed to pull image %s", image)
			}
			log.Infof("pull image %s success", image)
		}
	case "":
		log.Warn("no available container runtime found")
		return errors.New("no available container runtime found")
	default:
		return errors.Errorf("unsupported container runtime type %s", runtime.DetectRuntime())
	}
	return nil
}

// runtimeConfig holds container runtime configuration
type runtimeConfig struct {
	containerRuntime   string
	lowLevelRuntime    string
	dataRoot           string
	cgroupDriver       string
	enableDockerTls    bool
	insecureRegistries []string
}

// loadRuntimeConfig loads container runtime configuration from bkeConfig
func (ep *EnvPlugin) loadRuntimeConfig() runtimeConfig {
	cfg := runtimeConfig{
		insecureRegistries: defaultInsecureRegistries,
	}
	if ep.bkeConfig == nil {
		return cfg
	}
	cfg.containerRuntime = ep.bkeConfig.Cluster.ContainerRuntime.CRI
	cfg.lowLevelRuntime = ep.bkeConfig.Cluster.ContainerRuntime.Runtime
	if ep.bkeConfig.Cluster.ContainerRuntime.Param != nil {
		if v, ok := ep.bkeConfig.Cluster.ContainerRuntime.Param["data-root"]; ok {
			cfg.dataRoot = v
		}
		if v, ok := ep.bkeConfig.Cluster.ContainerRuntime.Param["cgroup-driver"]; ok {
			cfg.cgroupDriver = v
		} else {
			cfg.cgroupDriver = bkeinit.DefaultCgroupDriver
		}
		if v, ok := ep.bkeConfig.Cluster.ContainerRuntime.Param["insecure-registries"]; ok {
			cfg.insecureRegistries = append(cfg.insecureRegistries, strings.Split(v, ",")...)
			cfg.insecureRegistries = utils.UniqueStringSlice(cfg.insecureRegistries)
		}
	}
	if ep.bkeConfig.CustomExtra != nil {
		if v, ok := ep.bkeConfig.CustomExtra["pipelineServer"]; ok {
			if v == ep.currenNode.IP {
				cfg.enableDockerTls = true
			}
			if ep.currenNode.IP == ep.bkeConfig.CustomExtra["host"] {
				cfg.enableDockerTls = false
			}
		}
	}
	return cfg
}

// configAndRestartRuntime configures and restarts container runtime
func (ep *EnvPlugin) configAndRestartRuntime(cfg runtimeConfig, runtimeToUse string) error {
	if err := ep.configContainerRuntime(cfg, runtimeToUse); err != nil {
		return errors.Wrapf(err, "config container runtime %s failed", runtimeToUse)
	}
	if runtimeToUse == runtime.ContainerRuntimeContainerd {
		return nil
	}
	output, err := ep.exec.ExecuteCommandWithCombinedOutput("systemctl", "daemon-reload")
	if err != nil {
		return errors.Wrapf(err, "daemon-reload failed, output: %s", output)
	}
	output, err = ep.exec.ExecuteCommandWithCombinedOutput("systemctl", "reload", runtimeToUse)
	if err != nil {
		return errors.Wrapf(err, "reload container runtime %s failed, output: %s", runtimeToUse, output)
	}
	return nil
}

// initRuntime download container runtime
func (ep *EnvPlugin) initRuntime() error {
	cfg := ep.loadRuntimeConfig()

	//获取当前的containerRuntime
	currentContainerRuntime := runtime.DetectRuntime()

	// download richrunc if docker and richrunc
	if currentContainerRuntime == runtime.ContainerRuntimeDocker && cfg.lowLevelRuntime == "richrunc" {
		bkeCfg := bkeinit.BkeConfig(*ep.bkeConfig)
		url := bkesource.GetCustomDownloadPath(bkeCfg.YumRepo())
		url = fmt.Sprintf("url=%s/richrunc-%s", url, goruntime.GOARCH)

		commands := []string{
			downloader.Name,
			url,
			"chmod=755",
			"rename=runc",
			"saveto=/usr/local/beyondvm",
		}

		downloaderPlugin := downloader.New()
		if _, err := downloaderPlugin.Execute(commands); err != nil {
			return errors.Errorf("download richrunc %s failed, err: %v", url, err)
		}
	}
	// download cri-dockerd if docker
	if currentContainerRuntime == cfg.containerRuntime && currentContainerRuntime == runtime.ContainerRuntimeDocker {
		// 安装cri-dockerd
		if err := ep.downloadCriDockerd(); err != nil {
			return err
		}
	}

	// 指定了containerRuntime，但是和当前不一致，以指定的为准
	if cfg.containerRuntime != "" && cfg.containerRuntime != currentContainerRuntime {
		return ep.downloadContainerRuntime(cfg.containerRuntime, cfg.lowLevelRuntime, cfg.enableDockerTls, cfg.insecureRegistries)
	}

	// 指定了containerRuntime，和当前一致，修改配置文件
	if cfg.containerRuntime != "" && cfg.containerRuntime == currentContainerRuntime {
		return ep.configAndRestartRuntime(cfg, cfg.containerRuntime)
	}
	// 没有指定containerRuntime，但是当前有containerRuntime，以当前的为准,只配置
	if cfg.containerRuntime == "" && currentContainerRuntime != "" {
		return ep.configAndRestartRuntime(cfg, currentContainerRuntime)
	}
	return nil
}

func (ep *EnvPlugin) initDNS() error {
	if !utils.Exists(InitDNSConfPath) {
		if _, err := os.OpenFile(InitDNSConfPath, os.O_CREATE, RwxRwRw); err != nil {
			return errors.Wrapf(err, "create resolv.conf failed")
		}
	}
	if ep.machine.platform == "centos" {
		log.Infof("Turn off the function that the Network Manager automatically overwrites the resolv.conf file in centos")
		return ep.initNetworkManager()
	}
	return nil
}

// exportImageList export image list from bkeConfig
func (ep *EnvPlugin) exportImageList() []string {
	if ep.bkeConfig == nil {
		return nil
	}
	var images []string
	cfg := ep.bkeConfig
	conf := bkeinit.BkeConfig(*cfg)
	repo := fmt.Sprintf("%s/", strings.TrimRight(conf.ImageFuyaoRepo(), "/"))
	k8sVersion := strings.TrimPrefix(cfg.Cluster.KubernetesVersion, "v")
	etcdVersion := strings.TrimPrefix(cfg.Cluster.EtcdVersion, "v")

	exporter := imagehelper.NewImageExporter(repo, k8sVersion, etcdVersion)
	images, _ = exporter.ExportImageList()
	cNode := ep.currenNode
	if cNode.IP == "" {
		return images
	}
	if cNode.IsWorker() {
		return []string{}
	}
	return images
}

// getNTPServers get ntp servers from bkeConfig
func getNTPServer(cfg *bkev1beta1.BKEConfig) (string, error) {
	if cfg != nil && cfg.Cluster.NTPServer != "" {
		return cfg.Cluster.NTPServer, nil
	}
	return utils.GetNTPServerEnv()
}

func (ep *EnvPlugin) downloadContainerRuntime(containerRuntime, lowLevelRuntime string, enableDockerTls bool, insecureRegistries []string) error {
	switch containerRuntime {
	case runtime.ContainerRuntimeContainerd:
		return ep.downloadContainerd(lowLevelRuntime, insecureRegistries)
	case runtime.ContainerRuntimeDocker:
		return ep.downloadDocker(lowLevelRuntime, enableDockerTls, insecureRegistries)
	default:
		log.Errorf("unsupported container runtime type %s", containerRuntime)
		return errors.Errorf("unsupported container runtime type %s", containerRuntime)
	}
}

func (ep *EnvPlugin) downloadDocker(lowLevelRuntime string, enableDockerTls bool, insecureRegistries []string) error {
	if ep.bkeConfig != nil {
		cfg := bkeinit.BkeConfig(*ep.bkeConfig)
		dataRoot := bkeinit.DefaultCRIDockerDataRootDir
		cgroupDriver := bkeinit.DefaultCgroupDriver
		if cfg.Cluster.ContainerRuntime.Param != nil {
			if v, ok := cfg.Cluster.ContainerRuntime.Param["data-root"]; ok {
				dataRoot = v
			}
			if v, ok := cfg.Cluster.ContainerRuntime.Param["cgroup-driver"]; ok {
				cgroupDriver = v
			}
		}

		runtimeUrl := ""
		if lowLevelRuntime == "richrunc" {
			baseDownloadUrl := bkesource.GetCustomDownloadPath(cfg.YumRepo())
			runtimeUrl = fmt.Sprintf("%s/richrunc-%s", baseDownloadUrl, goruntime.GOARCH)
		}

		command := []string{
			dockerPlugin.Name,
			fmt.Sprintf("runtime=%s", lowLevelRuntime),
			fmt.Sprintf("dataRoot=%s", dataRoot),
			fmt.Sprintf("cgroupDriver=%s", cgroupDriver),
			fmt.Sprintf("enableDockerTls=%t", enableDockerTls),
			fmt.Sprintf("tlsHost=%s", ep.currenNode.IP),
			fmt.Sprintf("runtimeUrl=%s", runtimeUrl),
			fmt.Sprintf("insecureRegistries=%s", strings.Join(insecureRegistries, ",")),
		}

		dp := dockerPlugin.New(ep.exec)
		if _, err := dp.Execute(command); err != nil {
			log.Errorf("download docker failed, err: %s", err)
			return errors.Wrap(err, "download docker failed")
		}
		log.Info("download docker success")

		// 安装cri-dockerd
		if err := ep.downloadCriDockerd(); err != nil {
			return err
		}

		return nil
	}
	return errors.New("failed to download docker, bkeConfig is nil")
}

func (ep *EnvPlugin) downloadContainerd(lowLevelRuntime string, insecureRegistries []string) error {
	// todo 适配ContainerRuntime配置
	if ep.bkeConfig != nil {
		cfg := bkeinit.BkeConfig(*ep.bkeConfig)
		baseUrl := bkesource.GetCustomDownloadPath(cfg.YumRepo())
		repo := cfg.ImageThirdRepo()
		sandboxImage := fmt.Sprintf("%s/pause:%s", strings.TrimRight(repo, "/"),
			bkeinit.DefaultPauseImageTag)

		gzName := fmt.Sprintf("containerd-%s-linux-%s.tar.gz", cfg.Cluster.ContainerdVersion, goruntime.GOARCH)
		gzName = strings.ReplaceAll(gzName, "{.arch}", goruntime.GOARCH)
		url := fmt.Sprintf("%s/%s", baseUrl, gzName)

		dataRoot := bkeinit.DefaultCRIContainerdDataRootDir
		if ep.bkeConfig.Cluster.ContainerRuntime.Param != nil {
			if v, ok := ep.bkeConfig.Cluster.ContainerRuntime.Param["data-root"]; ok {
				dataRoot = v
			}
		}

		command := []string{
			containerdPlugin.Name,
			fmt.Sprintf("url=%s", url),
			fmt.Sprintf("sandbox=%s", sandboxImage),
			fmt.Sprintf("repo=%s", bkevalidte.GetImageRepoAddress(cfg.Cluster.ImageRepo)),
			fmt.Sprintf("runtime=%s", lowLevelRuntime),
			fmt.Sprintf("dataRoot=%s", dataRoot),
			fmt.Sprintf("insecureRegistries=%s", strings.Join(insecureRegistries, ",")),
		}
		if cfg.Cluster.ContainerdConfigRef != nil {
			command = append(command, fmt.Sprintf("containerdConfig=%s:%s", cfg.Cluster.ContainerdConfigRef.Namespace, cfg.Cluster.ContainerdConfigRef.Name))
		}
		cp := containerdPlugin.New(ep.exec)
		if _, err := cp.Execute(command); err != nil {
			log.Errorf("failed to run containerd plugin, err: %s", err)
			return errors.Wrap(err, "failed to run containerd plugin ")
		}
		log.Infof("download containerd %q success", gzName)
		return nil
	}
	return errors.New("bke config not found")
}

func (ep *EnvPlugin) downloadCriDockerd() error {
	if ep.bkeConfig != nil {
		cfg := bkeinit.BkeConfig(*ep.bkeConfig)

		sandboxImage := ""
		criDockerdUrl := ""
		k8sVersion := cfg.Cluster.KubernetesVersion
		v, err := semver.ParseTolerant(k8sVersion)
		if err != nil {
			return errors.Errorf("parse kubernetes version %s failed", k8sVersion)
		}

		// cri-dockerd only support k8s >= 1.24
		if v.LT(semver.MustParse("1.24.0")) {
			return nil
		}
		repo := cfg.ImageFuyaoRepo()
		exporter := imagehelper.NewImageExporter(repo, k8sVersion, "")
		imageMap, _ := exporter.ExportImageMap()
		sandboxImage = imageMap[bkeinit.DefaultPauseImageName]
		baseDownloadUrl := bkesource.GetCustomDownloadPath(cfg.YumRepo())
		criDockerdUrl = fmt.Sprintf("%s/cri-dockerd-0.3.9-%s", baseDownloadUrl, goruntime.GOARCH)

		command := []string{
			cridocker.Name,
			fmt.Sprintf("sandbox=%s", sandboxImage),
			fmt.Sprintf("criDockerdUrl=%s", criDockerdUrl),
		}

		cdp := cridocker.New(ep.exec)
		if _, err = cdp.Execute(command); err != nil {
			log.Errorf("download cri-dockerd failed, err: %s", err)
			return errors.Wrap(err, "download cri-dockerd failed")
		}

		log.Infof("download cri-dockerd success")
		return nil
	}

	return errors.New("failed to download cri-dockerd, bkeConfig is nil")
}

func (ep *EnvPlugin) configContainerRuntime(cfg runtimeConfig, runtimeToUse string) error {
	dataRoot := cfg.dataRoot
	switch runtimeToUse {
	case runtime.ContainerRuntimeDocker:
		if dataRoot == "" {
			dataRoot = bkeinit.DefaultCRIDockerDataRootDir
		}
		return edocker.ConfigDockerDaemon(edocker.DockerDaemonConfig{
			CgroupDriver:       cfg.cgroupDriver,
			LowLevelRuntime:    cfg.lowLevelRuntime,
			DataRoot:           dataRoot,
			EnableTls:          cfg.enableDockerTls,
			TlsHost:            ep.currenNode.IP,
			InsecureRegistries: cfg.insecureRegistries,
		})
	case runtime.ContainerRuntimeContainerd:
		if dataRoot == "" {
			dataRoot = bkeinit.DefaultCRIContainerdDataRootDir
		}
		// todo 适配ContainerRuntime配置
		return nil
	default:
		log.Errorf("unsupported container runtime type %s", runtimeToUse)
		return errors.Errorf("unsupported container runtime type %s", runtimeToUse)
	}
}

func (ep *EnvPlugin) initHttpRepo() error {
	if ep.bkeConfig == nil {
		return errors.New("bke config not found")
	}

	cfg := bkeinit.BkeConfig(*ep.bkeConfig)
	if cfg.Cluster.ImageRepo.Domain == "cr.openfuyao.cn" {
		log.Errorf("online deploy, not need mod repo")
		return nil
	}

	yumRepo := cfg.YumRepo()
	if yumRepo == "" {
		return errors.New("no http repo config found in bke config")
	}

	if err := bkesource.SetSource(yumRepo); err != nil {
		log.Errorf("set http repo failed, err: %s", err)
		return nil
	}

	if err := httprepo.RepoUpdate(); err != nil {
		currentContainerRuntime := runtime.DetectRuntime()
		if currentContainerRuntime == bkeinit.CRIDocker || currentContainerRuntime == bkeinit.CRIContainerd {
			log.Warnf("update http repo failed, err: %s", err)
			return nil
		}
		log.Errorf("update http repo failed, err: %s", err)
		return err
	}

	log.Infof("set http repo %q success", yumRepo)
	return nil
}

func (ep *EnvPlugin) initIptables() error {
	//开放接入端
	//开放输出端
	//开放中转端

	//check iptables command exit
	out, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "iptables -V")
	if err != nil {
		log.Warnf("iptables command not found, err: %s, output: %s", err, out)
		return nil
	}

	output, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "iptables -P INPUT ACCEPT")
	if err != nil {
		log.Warnf("iptables -P INPUT ACCEPT failed, err: %s, output: %s", err, output)
	}
	output, err = ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "iptables -P OUTPUT ACCEPT")
	if err != nil {
		log.Warnf("iptables -P OUTPUT ACCEPT failed, err: %s, output: %s", err, output)
	}
	output, err = ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "iptables -P FORWARD ACCEPT")
	if err != nil {
		log.Warnf("iptables -P FORWARD ACCEPT failed, err: %s, output: %s", err, output)
	}
	if ep.machine.platform == "Kylin" && ep.machine.hostArch == "arm64" {
		output, err = ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "iptables -I INPUT 1 -p vrrp -j ACCEPT")
		if err != nil {
			log.Warnf("iptables -A INPUT -p tcp --dport 6443 -j ACCEPT failed, err: %s, output: %s", err, output)
		}
	}

	return nil
}

func (ep *EnvPlugin) initRegistry() error {
	port := bkeinit.DefaultImageRepoPort
	if ep.bkeConfig != nil {
		if ep.bkeConfig.Cluster.ImageRepo.Port != "" {
			port = ep.bkeConfig.Cluster.ImageRepo.Port
		}
	}
	log.Infof("%s", port)
	return nil
}

// installNfsUtilIfNeeded installs nfs-utils if pipeline server node
func (ep *EnvPlugin) installNfsUtilIfNeeded() {
	if ep.bkeConfig == nil || ep.bkeConfig.CustomExtra == nil {
		return
	}
	v, ok := ep.bkeConfig.CustomExtra["pipelineServer"]
	if !ok || v != ep.currenNode.IP {
		return
	}

	nfsUtil := "nfs-utils"
	if ep.machine.platform == "ubuntu" {
		nfsUtil = "nfs-common"
	}
	if err := httprepo.RepoInstall(nfsUtil); err != nil {
		log.Warnf("failed install %s for pipeline server node, err: %v", nfsUtil, err)
	}
}

// installLxcfs installs lxcfs based on platform
func (ep *EnvPlugin) installLxcfs() error {
	if !utils.Exists("/var/lib/lxc/lxcfs") {
		if err := os.MkdirAll("/var/lib/lxc/lxcfs", RwxRxRx); err != nil {
			log.Errorf("failed create lxcfs dir, err: %v", err)
			return nil
		}
	}

	switch ep.machine.platform {
	case "centos":
		if strings.HasPrefix(ep.machine.version, "7") {
			log.Infof("install lxcfs for centos7")
			if err := httprepo.RepoInstall("fuse-libs", "lxcfs"); err != nil {
				log.Warnf("failed install lxcfs for centos7, err: %v", err)
			}
		}
		if strings.HasPrefix(ep.machine.version, "8") {
			log.Infof("install lxcfs for centos8")
			if err := httprepo.RepoInstall("lxcfs"); err != nil {
				log.Warnf("failed install lxcfs for centos8, err: %v", err)
			}
		}
	case "kylin":
		log.Infof("install lxcfs for kylin")
		if err := httprepo.RepoInstall("lxcfs", "lxcfs-tools"); err != nil {
			log.Warnf("failed install lxcfs for kylin, err: %v", err)
		}
	case "ubuntu":
		log.Infof("install lxcfs for ubuntu")
		if err := httprepo.RepoInstall("lxcfs"); err != nil {
			log.Warnf("failed install lxcfs for ubuntu, err: %v", err)
		}
	default:
		log.Warnf("not support platform: %s", ep.machine.platform)
		return nil
	}
	return nil
}

// configureLxcfsService configures and starts lxcfs service
func (ep *EnvPlugin) configureLxcfsService() error {
	if !utils.Exists("/usr/lib/systemd/system/lxcfs.service") {
		return nil
	}

	str1, err := os.ReadFile("/usr/lib/systemd/system/lxcfs.service")
	if err != nil {
		log.Warnf("failed read lxcfs service err: %v", err)
		return nil
	}

	str2 := bytes.ReplaceAll(str1, []byte("/var/lib/lxcfs"), []byte("/var/lib/lxc/lxcfs"))
	if err = os.WriteFile("/usr/lib/systemd/system/lxcfs.service", str2, RwRR); err != nil {
		log.Warnf("failed write lxcfs service err: %v", err)
		return nil
	}

	systemctl, err := initsystem.GetInitSystem()
	if err != nil {
		log.Warnf("failed get init system, err: %v", err)
		return nil
	}

	if err := systemctl.ServiceEnable("lxcfs"); err != nil {
		log.Warnf("failed enable lxcfs, err: %v", err)
	}
	if err := systemctl.ServiceRestart("lxcfs"); err != nil {
		log.Warnf("failed start lxcfs, err: %v", err)
	}
	return nil
}

// Deprecated 将在addon项目中处理额外依赖
func (ep *EnvPlugin) initExtra() error {
	ep.installNfsUtilIfNeeded()

	out1, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "umask 0022")
	if err != nil {
		log.Warnf("umask 0022 failed, err: %s, output: %s", err, out1)
	}

	if err := ep.installLxcfs(); err != nil {
		return err
	}

	out, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "modprobe fuse")
	if err != nil {
		log.Warnf("modprobe fuse failed, err: %s, output: %s", err, out)
	}

	return ep.configureLxcfsService()
}

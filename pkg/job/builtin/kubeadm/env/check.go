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
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	kerrors "k8s.io/apimachinery/pkg/util/errors"

	bkeinit "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/initialize"
	bkenode "gopkg.openfuyao.cn/cluster-api-provider-bke/common/cluster/node"
	bkesource "gopkg.openfuyao.cn/cluster-api-provider-bke/common/source"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/crontab"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/runtime"
)

const (
	two   = 2
	three = 3
)

// processSimpleCheckScope processes simple check scopes that only call one function
func (ep *EnvPlugin) processSimpleCheckScope(logMsg string, checkFunc func() error) error {
	log.Infof(logMsg)
	return checkFunc()
}

// processCheckScope processes a single check scope and returns error
func (ep *EnvPlugin) processCheckScope(scope string) error {
	switch scope {
	case "kernel":
		log.Infof("Check kernel param ...")
		if err := ep.checkKernelParam(); err != nil {
			log.Warnf("(ignore)Check kernel param failed: %v", err)
		}
		return nil
	case "firewall":
		return ep.processSimpleCheckScope("Check firewall disabled ...", ep.checkFirewall)
	case "selinux":
		return ep.processSimpleCheckScope("Check selinux disabled ...", ep.checkSelinux)
	case "swap":
		return ep.processSimpleCheckScope("Check swap disabled ...", ep.checkSwap)
	case "time":
		return ep.processSimpleCheckScope("Check time sync cron job is running...", ep.checkTime)
	case "hosts":
		return ep.processSimpleCheckScope("Check hosts file ...", ep.checkHost)
	case "ports":
		return ep.processSimpleCheckScope("Check ports is available ...", ep.checkHostPort)
	case "node":
		log.Info("Check node resources is adequate ...")
		return ep.checkNodeInfo()
	case "runtime":
		log.Info("Check container runtime is available ...")
		return ep.checkRuntime()
	case "dns":
		log.Info("Check dns is available ...")
		return ep.checkDNS()
	case "httpRepo":
		log.Info("[skip] Check http repo is available ...")
		return nil
	default:
		log.Warnf("Unknown check scope: %s, skipping", scope)
		return nil
	}
}

// checkK8sEnv check k8s environment
func (ep *EnvPlugin) checkK8sEnv() error {
	var checkErrs []error
	for _, s := range strings.Split(ep.scope, ",") {
		if err := ep.processCheckScope(s); err != nil {
			checkErrs = append(checkErrs, err)
		}
	}
	if len(checkErrs) > 0 {
		return kerrors.NewAggregate(checkErrs)
	}
	return nil
}

// checkNodeInfo use to check node info
// K8sEnvInit , check is true, scope is node
func (ep *EnvPlugin) checkNodeInfo() error {
	machine := ep.machine
	machine.logInfo()

	cNode := ep.currenNode
	if cNode.IP == "" {
		log.Warnf("Current node info is empty")
		return errors.New("current node info is empty, bkeConfig may not be set")
	}
	log.Infof("HOST_IP  : %s", cNode.IP)

	var errs []error

	if !utils.ContainsString(utils.GetSupportPlatforms(), machine.platform) {
		log.Warnf("The current host system is %s, bke only support %v", machine.platform, utils.GetSupportPlatforms())
	}

	switch {
	case utils.ContainsString(cNode.Role, bkenode.MasterNodeRole):
		if machine.cpuNum < two {
			log.Warnf("CPU is not enough, at least need 2, but got %d", machine.cpuNum)
			errs = append(errs, errors.Errorf("the system number of available CPUs %d is less than the minimum required %d", machine.cpuNum, utils.MinControlPlaneNumCPU))
		}
		if machine.memSize < utils.MinControlPlaneMem {
			log.Warnf("Memory is not enough, at least need %dGB, but got %dGB", utils.MinControlPlaneMem, machine.memSize)
			errs = append(errs, errors.Errorf("the system RAM (%d GB) is less than the minimum required %d GB", machine.memSize, utils.MinControlPlaneMem))
		}
	default:
		// Worker node or unknown role, skip resource check
		log.Debugf("Node role is %v, skipping master node resource check", cNode.Role)
	}
	if len(errs) == 0 {
		log.Infof("Node resources is adequate")
		return nil
	}
	return kerrors.NewAggregate(errs)
}

// checkFileLimitConfig checks file limit configuration
func (ep *EnvPlugin) checkFileLimitConfig() []error {
	var errs []error
	if !utils.Exists(InitFileLimitConfPath) {
		log.Warnf("File %s not exists", InitFileLimitConfPath)
		errs = append(errs, errors.Errorf("file %s not exists", InitFileLimitConfPath))
		return errs
	}

	if found, err := catAndSearch(InitFileLimitConfPath, "", HardLimitsRegex); err != nil || !found {
		log.Warnf("File %s not contains %s", InitFileLimitConfPath, HardLimitsRegex)
		errs = append(errs, errors.Errorf("file %q content not match regex %q", InitFileLimitConfPath, HardLimitsRegex))
	}
	if found, err := catAndSearch(InitFileLimitConfPath, "", SoftLimitsRegex); err != nil || !found {
		log.Warnf("File %s not contains %s", InitFileLimitConfPath, SoftLimitsRegex)
		errs = append(errs, errors.Errorf("file %q content not match regex %q", InitFileLimitConfPath, SoftLimitsRegex))
	}
	return errs
}

// checkUbuntuSysModules checks Ubuntu system modules
func (ep *EnvPlugin) checkUbuntuSysModules() []error {
	var errs []error
	if ep.machine.platform != "ubuntu" {
		return errs
	}

	if !utils.Exists(CheckUbuntuSysModuleFilePath) {
		log.Warnf("%s not found", CheckUbuntuSysModuleFilePath)
		errs = append(errs, errors.Errorf("%s not found", CheckUbuntuSysModuleFilePath))
		return errs
	}

	log.Infof("%s found", CheckUbuntuSysModuleFilePath)
	for _, m := range sysModule {
		if found, err := catAndSearch(CheckUbuntuSysModuleFilePath, m, ""); err != nil || !found {
			log.Warnf("%s not contains %s", CheckUbuntuSysModuleFilePath, m)
			errs = append(errs, errors.Errorf("%s not contains %s", CheckUbuntuSysModuleFilePath, m))
		}
	}
	return errs
}

// checkCentosKylinSysModules checks CentOS/Kylin system modules
func (ep *EnvPlugin) checkCentosKylinSysModules() []error {
	var errs []error
	if ep.machine.platform != "centos" && ep.machine.platform != "kylin" {
		return errs
	}

	if !utils.Exists(CheckIpvsSysModuleFilePath) {
		log.Warnf("%s not found", CheckIpvsSysModuleFilePath)
		errs = append(errs, errors.Errorf("%s not found", CheckIpvsSysModuleFilePath))
		return errs
	}

	log.Infof("%s found", CheckIpvsSysModuleFilePath)
	return errs
}

// checkKylinRcLocal checks Kylin rc.local file
func (ep *EnvPlugin) checkKylinRcLocal() []error {
	var errs []error
	if ep.machine.platform != "kylin" {
		return errs
	}

	found, err := catAndSearch(rcLoaclFilePath, CheckIpvsSysModuleFilePath, "")
	if err != nil {
		log.Warnf("check rc.local failed, err: %v", err)
		errs = append(errs, err)
	}
	if !found {
		log.Warnf("%s not found in %s", CheckIpvsSysModuleFilePath, rcLoaclFilePath)
		errs = append(errs, errors.Errorf("%s not found in %s", CheckIpvsSysModuleFilePath, rcLoaclFilePath))
	}
	return errs
}

// checkBridge check kernel net bridge param
func (ep *EnvPlugin) checkKernelParam() error {
	var checkErrs []error

	checkErrs = append(checkErrs, ep.checkFileLimitConfig()...)

	// Setup kernel parameter for CentOS 7 with containerd
	if ep.bkeConfig != nil && ep.machine.platform == "centos" &&
		ep.bkeConfig.Cluster.ContainerRuntime.CRI == bkeinit.CRIContainerd &&
		strings.HasPrefix(ep.machine.version, "7") {
		execKernelParam["fs.may_detach_mounts"] = "1"
	}

	// Check all kernel parameters
	for k, v := range execKernelParam {
		pathEum := append([]string{procSysPath}, strings.Split(k, ".")...)
		path := filepath.Join(pathEum...)
		if _, err := catAndSearch(path, v, ""); err != nil {
			checkErrs = append(checkErrs, errors.Wrapf(err, "kernel param %s=%s failed", k, v))
		}
		log.Infof("Kernel param %s=%s passed", k, v)
	}

	if len(checkErrs) == 0 {
		log.Infof("Kernel param check passed")
	}

	checkErrs = append(checkErrs, ep.checkUbuntuSysModules()...)
	checkErrs = append(checkErrs, ep.checkCentosKylinSysModules()...)
	checkErrs = append(checkErrs, ep.checkKylinRcLocal()...)

	return kerrors.NewAggregate(checkErrs)
}

// checkFirewall check firewall is disabled
func (ep *EnvPlugin) checkFirewall() error {
	var checkErrs []error
	// It has been tested and verified that if this command fails to match the "dead", an error of exit status 1 will be returned
	output, err := ep.exec.ExecuteCommandWithCombinedOutput("/bin/sh", "-c", "systemctl status firewalld | grep dead")
	if err != nil {
		if !strings.Contains(output, "not loaded") && !strings.Contains(output, "not be found") {
			checkErrs = append(checkErrs, errors.Wrap(err, "Check firewalld failed"))
		}
		log.Warnf("Check firewall failed, err: %v, out: %s", err, output)
	}
	if !strings.Contains(output, "not loaded") && !strings.Contains(output, "not be found") && output == "" {
		log.Warn("Check firewall failed, err: firewalld not dead")
		checkErrs = append(checkErrs, errors.New("firewalld not dead"))
	}
	if len(checkErrs) == 0 {
		log.Infof("firewalld is disabled")
		return nil
	}
	return kerrors.NewAggregate(checkErrs)
}

// checkSelinux check selinux
func (ep *EnvPlugin) checkSelinux() error {
	// skip ubuntu
	if ep.machine.platform == "ubuntu" {
		return nil
	}
	// todo 目前检查selinux关闭 只是看下配置文件是否修改，另外的检查方式只有命令，
	// todo 但是在init时如果 setenforce 0 执行成功,且配置文件修改成功，无论是否重启服务器selinux都是关闭的
	// todo 如果 setenforce 0 执行失败则不会修改配置文件，此处便会判定失败
	if _, err := catAndSearch(CheckSelinuxConfPath, "SELINUX=disabled", ""); err != nil {
		log.Warnf("(ignore)Check seLinux config file %s failed, err: ", CheckSelinuxConfPath, err)
	}
	out, err := ep.exec.ExecuteCommandWithOutput("/bin/sh", "-c", "getenforce")
	if err != nil && (out != "Disabled" && out != "Permissive") {
		log.Warnf("Check seLinux failed, err: %v, out: %s", err, out)
	}
	log.Infof("seLinux is disabled")
	return nil
}

// checkSwap check swap
func (ep *EnvPlugin) checkSwap() error {
	if _, err := catAndSearch(CheckSwapConfPath, "", SwapRegex); err != nil {
		log.Warnf("Check swap failed, %s", err)
		return errors.Wrap(err, "Check swap failed")
	}
	log.Infof("swap is disabled")
	return nil
}

// checkTime check time sync
func (ep *EnvPlugin) checkTime() error {
	if !crontab.FindSyncTimeJob() {
		log.Warnf("Time sync job not found, please check time sync")
		return nil
	}
	log.Infof("Time sync cron job found")
	return nil
}

// checkHost check host
func (ep *EnvPlugin) checkHost() error {
	hostname, err := os.Hostname()
	if err != nil {
		return errors.Wrap(err, "Get hostname failed when init hostanme")
	}
	bkeNodeName := utils.HostName()
	if hostname != bkeNodeName {
		log.Errorf("Hostname is not match, current hostname is %s, except hostname is %s", hostname, bkeNodeName)
		return errors.Errorf("Hostname is not match, current hostname is %s, except hostname is %s", hostname, bkeNodeName)
	}

	h, err := NewHostsFile(CheckHostConfPath)
	if err != nil {
		log.Errorf("Check hosts file failed, %s", err)
		return errors.Wrap(err, "check hosts file failed, get hosts file failed")
	}
	extraHosts := strings.Split(ep.extraHosts, ",")
	extraHosts = append(extraHosts, ep.clusterHosts...)
	if len(extraHosts) == 0 {
		return nil
	}

	// one host is not in hosts file ,then flag is false
	for _, host := range extraHosts {
		flag := false
		if host == "" {
			continue
		}
		for _, record := range h.inner.Records() {
			hostWithIP := strings.Split(strings.TrimSpace(host), ":")
			if len(hostWithIP) != two {
				return errors.Errorf("check hosts failed,hosts format error, host: %s", host)
			}
			// default ip type use "ip"
			addr, err := net.ResolveIPAddr("ip", hostWithIP[1])
			if err != nil {
				return errors.Wrapf(err, "check hosts failed,resolve ip address %s failed,not a valid ip address", hostWithIP[1])
			}
			if MatchProtocols(record.IpAddress.IP, addr.IP) && record.IpAddress.IP.Equal(addr.IP) {
				flag = true
				break
			}
		}
		if !flag {
			log.Errorf("check hosts failed, host: %s not in hosts file", host)
			return errors.Errorf("check hosts failed,hosts not match, hosts: %s", ep.extraHosts)
		}
	}
	log.Infof("check hosts success")
	return nil
}

// checkHostPort check host port
func (ep *EnvPlugin) checkHostPort() error {
	var errs []error
	//todo worker 节点不检查部分端口
	for _, port := range ep.hostPort {
		target := net.JoinHostPort("127.0.0.1", port)
		conn1, err1 := net.DialTimeout("tcp", target, three*time.Second)
		if err1 != nil {
			log.Infof("check host port failed, port: %s, err: %s", port, err1.Error())
			errs = append(errs, errors.Wrapf(err1, "port: %s", port))
		}
		err := getPortOpenResult(err1, conn1)
		if err != nil {
			log.Infof("check host port failed, port: %s, err: %s", port, err.Error())
			errs = append(errs, errors.Wrapf(err, "port: %s", port))
		}
		log.Infof("host port %s is available", port)
	}
	if len(errs) == 0 {
		log.Infof("All required ports are available")
		return nil
	}
	return kerrors.NewAggregate(errs)
}

// checkRuntime check runtime
func (ep *EnvPlugin) checkRuntime() error {
	var containerRuntime string
	if ep.bkeConfig != nil {
		containerRuntime = ep.bkeConfig.Cluster.ContainerRuntime.CRI
	}
	//获取当前的containerRuntime
	currentContainerRuntime := runtime.DetectRuntime()

	if containerRuntime != "" && currentContainerRuntime != "" && containerRuntime != currentContainerRuntime {
		return errors.Errorf("container runtime is not match, except container runtime is %q, current container runtime is %q", containerRuntime, currentContainerRuntime)
	}
	if containerRuntime == "" && currentContainerRuntime == "" {
		return errors.New("no available container runtime found")
	}
	return nil
}

func (ep *EnvPlugin) checkDNS() error {
	if !utils.Exists(CheckDNSConfPath) {
		return errors.Errorf("check dns failed, %s not exists", CheckDNSConfPath)
	}
	if ep.machine.hostOS == "centos" {

	}
	return nil
}

func (ep *EnvPlugin) checkHttpRepo() error {
	if ep.bkeConfig != nil {
		cfg := bkeinit.BkeConfig(*ep.bkeConfig)
		httpRepo := cfg.YumRepo()
		if httpRepo == "" {
			return nil
		}

		if ep.machine.platform == "ubuntu" {
			url, err := bkesource.GetRPMDownloadPath(httpRepo)
			if err != nil {
				return err
			}
			found, err := catAndSearch("/etc/apt/sources.list", url, "")
			if err != nil {
				return err
			}
			if !found {
				return errors.New("check http repo failed, bke repo not found")
			}
			return nil
		}

		out, err := ep.exec.ExecuteCommandWithCombinedOutput("sh", "-c", "yum repolist")
		if err != nil {
			return errors.Wrap(err, "check http repo failed")
		}
		if !strings.Contains(out, "bke") {
			return errors.New("check http repo failed, bke repo not found")
		}
		return nil
	}

	return errors.New("bke config not found")
}

// getPortOpenResult get port open result
func getPortOpenResult(err1 error, conn1 net.Conn) error {
	flag1 := false

	if err1 != nil {
		flag1 = false
	}
	if conn1 != nil {
		flag1 = true
		err := conn1.Close()
		if err != nil {
			return errors.Wrap(err, "close connection failed")
		}
	} else {
		flag1 = false
	}
	if flag1 {
		return nil
	}
	return errors.New("port is not open")
}

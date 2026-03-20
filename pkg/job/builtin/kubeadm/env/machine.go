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
	"runtime"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/log"
	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/bkeagent/option"
)

type Machine struct {
	Hostname string
	hostArch string
	hostOS   string
	platform string
	version  string
	kernel   string
	cpuNum   int
	memSize  int
}

func NewMachine() *Machine {
	h, _ := host.Info()
	c, _ := cpu.Counts(false)
	v, _ := mem.VirtualMemory()
	m := v.Total/1024/1024/1024 + 1

	machine := &Machine{
		Hostname: h.Hostname,
		hostArch: runtime.GOARCH,
		hostOS:   runtime.GOOS,
		platform: h.Platform,
		version:  h.PlatformVersion,
		kernel:   h.KernelVersion,
		cpuNum:   c,
		memSize:  int(m),
	}
	if option.Platform != "" {
		machine.platform = option.Platform
	}
	if option.Version != "" {
		machine.version = option.Version
	}

	return machine
}

func (m *Machine) logInfo() {
	log.Infof("HOST_NAME: %s", m.Hostname)
	log.Infof("PLATFORM : %s", m.platform)
	log.Infof("VERSION  : %s", m.version)
	log.Infof("KERNEL   : %s", m.kernel)
	log.Infof("OS       : %s", m.hostOS)
	log.Infof("ARCH     : %s", m.hostArch)
	log.Infof("CPU      : %d", m.cpuNum)
	log.Infof("MEMORY   : %d", m.memSize)
}

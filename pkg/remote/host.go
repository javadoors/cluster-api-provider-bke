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

package remote

import (
	"fmt"
	"net"
)

type Host struct {
	User               string
	Password           string
	Address            string
	Port               string
	SSHKey             interface{}
	Extra              map[string]string
	ExtraCustomCmdFunc func(host *Host) Command
}

type Hosts []Host

func (h *Host) Validate() (*Host, error) {
	// Validate the host's fields to ensure they are set correctly.
	if h.User == "" {
		return nil, fmt.Errorf("Host's user field is required ")
	}
	// At least one of Password or SSHKey must be provided.
	if h.Password == "" && h.SSHKey == "" {
		return nil, fmt.Errorf("At least one of the host's password and ssh key is provided ")
	}
	// Validate the host's address and port.
	if h.Address == "" {
		return nil, fmt.Errorf("Host address is required ")
	}
	if net.ParseIP(h.Address) == nil {
		return nil, fmt.Errorf("Host's address not a valid IP address ")
	}
	if h.Port == "" {
		return nil, fmt.Errorf("Host's port must be greater than zero ")
	}
	return h, nil
}

func (h Host) Fields() (string, string, string, string, interface{}) {
	return h.User, h.Password, h.Address, h.Port, h.SSHKey
}

func (h *Hosts) Remove(ip string) {
	for i, host := range *h {
		if host.Address == ip {
			(*h)[i] = (*h)[len(*h)-1]
			(*h) = (*h)[:len(*h)-1]
		}
	}
}

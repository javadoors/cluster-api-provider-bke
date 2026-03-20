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

	hostsfile "github.com/kevinburke/hostsfile/lib"
)

const (
	// RwRR is the permission of the file
	RwRR = 0644
	// RwxRxRx is the permission of the directory
	RwxRxRx = 0755
	RwxRwRw = 0766
	RwRwRw  = 0666
	RwRwR   = 0664
)

// HostsFile is a wrapper around the hostsfile.Hostsfile type.
// It provides convenient methods for reading, modifying, and writing host entries.
type HostsFile struct {
	inner hostsfile.Hostsfile
}

// NewHostsFile opens and parses the hosts file located at the given path.
// It returns a new HostsFile instance or an error if the file cannot be opened or decoded.
func NewHostsFile(path string) (*HostsFile, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, RwRwR)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h, err := hostsfile.Decode(f)
	if err != nil {
		return nil, err
	}
	return &HostsFile{inner: h}, nil
}

// Set adds or updates the given hostname with the specified IP address
// in the hosts file. If the hostname already exists, its IP address will be updated.
func (h *HostsFile) Set(ipa *net.IPAddr, hostname string) error {
	return h.inner.Set(*ipa, hostname)
}

// Remove deletes the specified hostname entry from the hosts file.
// It returns true if the hostname was found and removed, otherwise false.
func (h *HostsFile) Remove(hostname string) bool {
	return h.inner.Remove(hostname)
}

// WriteHostsFileTo writes the current hosts data to the specified file path.
// If the file does not exist, it will be created.
func (h *HostsFile) WriteHostsFileTo(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, RwRwRw)
	if err != nil {
		return err
	}
	defer f.Close()
	return hostsfile.Encode(f, h.inner)
}

// MatchProtocols returns true if both IP addresses a and b belong to the same protocol family.
// It returns true when both are IPv4 or both are IPv6 addresses.
func MatchProtocols(a, b net.IP) bool {
	return (a.To4() == nil && b.To4() == nil) || (a.To4() != nil && b.To4() != nil)
}

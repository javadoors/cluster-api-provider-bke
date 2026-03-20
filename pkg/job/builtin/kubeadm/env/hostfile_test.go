/*
 *
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 *
 */

package env

import (
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	hostIPSegA  = 192
	hostIPSegB  = 168
	hostIPSegC  = 1
	hostIPSegD1 = 10
	hostIPSegD2 = 100
	hostIPSegD3 = 101
	hostIPSegD4 = 102
)

var (
	hostIPv4A = net.IPv4(hostIPSegA, hostIPSegB, hostIPSegC, hostIPSegD1)
	hostIPv4B = net.IPv4(hostIPSegA, hostIPSegB, hostIPSegC, hostIPSegD2)
	hostIPv4C = net.IPv4(hostIPSegA, hostIPSegB, hostIPSegC, hostIPSegD3)
	hostIPv4D = net.IPv4(hostIPSegA, hostIPSegB, hostIPSegC, hostIPSegD4)
)

func TestNewHostsFileSuccess(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "hosts.*")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = tmpFile.WriteString("")
	assert.NoError(t, err)

	hf, err := NewHostsFile(tmpFile.Name())

	assert.NoError(t, err)
	assert.NotNil(t, hf)
}

func TestNewHostsFileOpenError(t *testing.T) {
	hf, err := NewHostsFile("/nonexistent/path/to/hosts/file")

	assert.Error(t, err)
	assert.Nil(t, hf)
}

func TestHostsFileSetSuccess(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "hosts.*")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = tmpFile.WriteString("")
	assert.NoError(t, err)

	hf, err := NewHostsFile(tmpFile.Name())
	assert.NoError(t, err)

	ipAddr := &net.IPAddr{
		IP: hostIPv4A,
	}

	err = hf.Set(ipAddr, "testhost")

	assert.NoError(t, err)
}

func TestHostsFileRemoveFound(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "hosts.*")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = tmpFile.WriteString("")
	assert.NoError(t, err)

	hf, err := NewHostsFile(tmpFile.Name())
	assert.NoError(t, err)

	ipAddr := &net.IPAddr{
		IP: hostIPv4A,
	}

	err = hf.Set(ipAddr, "testhost")
	assert.NoError(t, err)

	result := hf.Remove("testhost")

	assert.True(t, result)
}

func TestHostsFileRemoveNotFound(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "hosts.*")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = tmpFile.WriteString("")
	assert.NoError(t, err)

	hf, err := NewHostsFile(tmpFile.Name())
	assert.NoError(t, err)

	result := hf.Remove("nonexistent")

	assert.False(t, result)
}

func TestHostsFileWriteSuccess(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "hosts.*")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = tmpFile.WriteString("")
	assert.NoError(t, err)

	hf, err := NewHostsFile(tmpFile.Name())
	assert.NoError(t, err)

	outputFile, err := os.CreateTemp("", "hosts_output.*")
	assert.NoError(t, err)
	defer os.Remove(outputFile.Name())
	defer outputFile.Close()

	err = hf.WriteHostsFileTo(outputFile.Name())

	assert.NoError(t, err)
}

func TestHostsFileWriteCreateError(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "hosts.*")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = tmpFile.WriteString("")
	assert.NoError(t, err)

	hf, err := NewHostsFile(tmpFile.Name())
	assert.NoError(t, err)

	err = hf.WriteHostsFileTo("/invalid/path/to/write")

	assert.Error(t, err)
}

func TestMatchProtocolsBothIPv4(t *testing.T) {
	ip1 := hostIPv4A
	ip2 := hostIPv4B

	result := MatchProtocols(ip1, ip2)

	assert.True(t, result)
}

func TestMatchProtocolsBothIPv6(t *testing.T) {
	ip1 := net.ParseIP("::1")
	ip2 := net.ParseIP("2001:db8::1")

	result := MatchProtocols(ip1, ip2)

	assert.True(t, result)
}

func TestMatchProtocolsIPv4AndIPv6(t *testing.T) {
	ip1 := hostIPv4A
	ip2 := net.ParseIP("2001:db8::1")

	result := MatchProtocols(ip1, ip2)

	assert.False(t, result)
}

func TestMatchProtocolsIPv6AndIPv4(t *testing.T) {
	ip1 := net.ParseIP("2001:db8::1")
	ip2 := hostIPv4A

	result := MatchProtocols(ip1, ip2)

	assert.False(t, result)
}

func TestMatchProtocolsNilIPv4(t *testing.T) {
	ip1 := net.IPv4zero
	ip2 := hostIPv4A

	result := MatchProtocols(ip1, ip2)

	assert.True(t, result)
}

func TestHostsFileSetAndWrite(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "hosts.*")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	_, err = tmpFile.WriteString("")
	assert.NoError(t, err)

	hf, err := NewHostsFile(tmpFile.Name())
	assert.NoError(t, err)

	ipAddr1 := &net.IPAddr{IP: hostIPv4A}
	ipAddr2 := &net.IPAddr{IP: hostIPv4B}

	err = hf.Set(ipAddr1, "node1")
	assert.NoError(t, err)

	err = hf.Set(ipAddr2, "node2")
	assert.NoError(t, err)

	outputFile, err := os.CreateTemp("", "hosts_written.*")
	assert.NoError(t, err)
	defer os.Remove(outputFile.Name())
	defer outputFile.Close()

	err = hf.WriteHostsFileTo(outputFile.Name())
	assert.NoError(t, err)

	content, err := os.ReadFile(outputFile.Name())
	assert.NoError(t, err)
	assert.Contains(t, string(content), "node1")
	assert.Contains(t, string(content), "node2")
}

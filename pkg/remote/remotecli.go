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
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/log"
)

type HostRemoteClient struct {
	SshClient  *Ssh
	SftpClient *Sftp
	host       *Host
	log        *zap.SugaredLogger
}

// NewRemoteClient returns a new remote client with ssh and sftp client
func NewRemoteClient(h *Host) (*HostRemoteClient, error) {
	h, err := h.Validate()
	if err != nil {
		return nil, errors.Wrap(err, "host validation failed")
	}
	c := &HostRemoteClient{
		host: h,
		log:  log.With("host", h.Address),
	}

	c.SshClient, err = NewSSHClient(c.host)
	if err != nil {
		return nil, errors.Errorf("Failed to create %s ssh client, err: %v", c.host.Address, err)
	}
	if c.SshClient == nil {
		return nil, errors.Errorf("Failed to create %s ssh client,err: %v", c.host.Address, err)
	}

	c.SftpClient, err = NewSFTPClient(c.SshClient.sshClient)
	if err != nil {
		return nil, errors.Errorf("Failed to create %s sftp client, err: %v", c.host.Address, err)
	}
	if c.SftpClient == nil {
		return nil, errors.Errorf("Failed to create %s sftp client, err: %v", c.host.Address, err)
	}
	return c, nil
}

func (c *HostRemoteClient) SetLogger(log *zap.SugaredLogger) {
	c.log = log.With("host", c.host.Address)
}

func (cli *HostRemoteClient) Exec(ctx context.Context, cmd Command, stdErrChan chan CombineOut, stdOutChan chan CombineOut) {

	hostIp := cli.host.Address

	defer func() {
		if err := recover(); err != nil {
			stdErrChan <- NewCombineOut(hostIp, "", fmt.Sprintf("%v", err))
		}
		return
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				if err := cli.CloseRemoteCli(); err != nil {
					stdErrChan <- NewCombineOut(hostIp, "", fmt.Sprintf("%v", err))
				}
				return
			}
		}
	}()

	for _, file := range cmd.FileUp {
		cli.log.Infof("Upload file %q to %q %q", file.Src, hostIp, file.Dst)
		if err := cli.SftpClient.UploadFile(file.Src, file.Dst); err != nil {
			errInfo := fmt.Sprintf("Failed to upload file %q to %q %q, err: %s", file.Src, hostIp, file.Dst, err.Error())
			cli.log.Debugf(errInfo)
			stdErrChan <- NewCombineOut(hostIp, "", errInfo)
			continue
		}
		cli.log.Debugf("Upload file %q to %q %q, success", file.Src, hostIp, file.Dst)
	}

	for _, c := range cmd.List() {
		cli.log.Infof("Execute command %q on %q", c, hostIp)
		stderr, stdout, err := cli.SshClient.Exec(c)
		if len(stderr) != 0 {
			errInfo := fmt.Sprintf("Failed to execute command %q on %q, stderr: %v", c, hostIp, stderr)
			cli.log.Debugf(errInfo)
			stdErrChan <- NewCombineOut(hostIp, c, errInfo)
		}
		if err != nil {
			errInfo := fmt.Sprintf("Failed to execute command %q on %q, err: %s", c, hostIp, err.Error())
			cli.log.Debugf(errInfo)
			stdErrChan <- NewCombineOut(hostIp, c, errInfo)
		}
		if len(stdout) != 0 {
			cli.log.Debugf("Execute command %q on %q, stdout: %s", c, hostIp, stdout)
			stdOutChan <- NewCombineOut(hostIp, c, strings.Join(stdout, "\n"))
		}
		if len(stderr) == 0 && err == nil {
			cli.log.Debugf("Execute command %q on %q, success", c, hostIp)
		}
	}

}

// CloseRemoteCli 关闭远程客户端
func (cli *HostRemoteClient) CloseRemoteCli() error {
	var errs []string
	if err := cli.SftpClient.Close(); err != nil && err != io.EOF {
		errs = append(errs, errors.Errorf("Failed to close %s sftp client, err: %v", cli.host.Address, err).Error())
	}
	if err := cli.SshClient.Close(); err != nil && err != io.EOF {
		errs = append(errs, errors.Errorf("Failed to close %s ssh client, err: %v", cli.host.Address, err).Error())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, ";"))
	}
	return nil
}

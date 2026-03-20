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

import "github.com/pkg/errors"

// initNetworkManager init network manager automatically overwrite resolv.conf file function
func (ep *EnvPlugin) initNetworkManager() error {
	if err := ep.bakFile(InitNetWorkManagerPath); err != nil {
		return err
	}
	src := "[main]"
	dst := "[main]\ndns=none"
	key := "dns=none"
	if ok, _ := catAndSearch(InitNetWorkManagerPath, key, ""); ok {
		return nil
	}
	if err := catAndReplace(InitNetWorkManagerPath, src, dst, ""); err != nil {
		return err
	}
	output, err := ep.exec.ExecuteCommandWithOutput("/bin/sh", "-c", "systemctl restart NetworkManager")
	if err != nil {
		return errors.Wrapf(err, "restart network manager failed, err: %s, output: %s", err, output)
	}
	return nil
}

func (ep *EnvPlugin) checkNetworkManager() error {
	key := "dns=none"
	_, err := catAndSearch(CheckNetWorkManagerPath, key, "")
	return err
}

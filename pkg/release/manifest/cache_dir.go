/*
 * Copyright (c) 2026 Huawei Technologies Co., Ltd.
 * openFuyao is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 */

package manifest

import (
	"os"
	"strings"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/config"
)

// DefaultReleaseCacheDir is the container path for release bundle yaml/file cache.
// Mount a hostPath volume at this path in the capbke Deployment.
const DefaultReleaseCacheDir = "/var/lib/bke/release-cache"

const releaseCacheDirEnvKey = "RELEASE_CACHE_DIR"

// ReleaseCacheDir returns the configured release bundle disk cache root.
// Priority: RELEASE_CACHE_DIR env > --release-cache-dir flag > DefaultReleaseCacheDir.
func ReleaseCacheDir() string {
	if dir := strings.TrimSpace(os.Getenv(releaseCacheDirEnvKey)); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(config.ReleaseCacheDir); dir != "" {
		return dir
	}
	return DefaultReleaseCacheDir
}

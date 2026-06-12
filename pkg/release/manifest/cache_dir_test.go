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
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/capbke/config"
)

func TestReleaseCacheDirDefaults(t *testing.T) {
	t.Setenv(releaseCacheDirEnvKey, "")
	config.ReleaseCacheDir = ""
	assert.Equal(t, DefaultReleaseCacheDir, ReleaseCacheDir())
}

func TestReleaseCacheDirFromEnv(t *testing.T) {
	t.Setenv(releaseCacheDirEnvKey, "/data/release-cache")
	config.ReleaseCacheDir = "/ignored"
	assert.Equal(t, "/data/release-cache", ReleaseCacheDir())
}

func TestReleaseCacheDirFromFlag(t *testing.T) {
	t.Setenv(releaseCacheDirEnvKey, "")
	config.ReleaseCacheDir = "/flag/release-cache"
	assert.Equal(t, "/flag/release-cache", ReleaseCacheDir())
}

func TestReleaseCacheDirEnvWins(t *testing.T) {
	t.Setenv(releaseCacheDirEnvKey, "/env/release-cache")
	config.ReleaseCacheDir = "/flag/release-cache"
	assert.Equal(t, "/env/release-cache", ReleaseCacheDir())
}

func TestMain(m *testing.M) {
	config.ReleaseCacheDir = ""
	os.Unsetenv(releaseCacheDirEnvKey)
	os.Exit(m.Run())
}

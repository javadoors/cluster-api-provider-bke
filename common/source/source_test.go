/*
 *
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain n copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 *
 */
package source

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testFilePermission = 0644
	testDirPermission  = 0755
)

func TestGetCustomDownloadPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "URL with trailing slash",
			input:    "http://example.com/repo/",
			expected: "http://example.com/repo/files",
		},
		{
			name:     "URL without trailing slash",
			input:    "http://example.com/repo",
			expected: "http://example.com/repo/files",
		},
		{
			name:     "URL with query parameters",
			input:    "http://example.com/repo?param=v",
			expected: "http://example.com/repo?param=v/files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetCustomDownloadPath(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWriteYumSource(t *testing.T) {
	tempDir := t.TempDir()
	originalYumRepos := yumRepos
	yumRepos = tempDir
	defer func() { yumRepos = originalYumRepos }()

	testURL := "http://test.repo.com/centos"
	err := writeYumSource(testURL)
	assert.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(yumRepos, "bke.repo"))
	assert.NoError(t, err)
	assert.Contains(t, string(content), testURL)
}

func TestWriteAptSource(t *testing.T) {
	tempDir := t.TempDir()
	originalAptRepos := aptRepos
	aptRepos = filepath.Join(tempDir, "sources.list")
	defer func() { aptRepos = originalAptRepos }()

	testURL := "http://test.repo.com/ubuntu"
	err := writeAptSource(testURL)
	assert.NoError(t, err)

	content, err := os.ReadFile(aptRepos)
	assert.NoError(t, err)
	assert.Contains(t, string(content), testURL)
}

func TestBackupAptSource(t *testing.T) {
	tempDir := t.TempDir()
	originalAptRepos := aptRepos
	aptRepos = filepath.Join(tempDir, "sources.list")
	defer func() { aptRepos = originalAptRepos }()

	err := os.WriteFile(aptRepos, []byte("test"), testFilePermission)
	assert.NoError(t, err)

	err = backupAptSource()
	assert.NoError(t, err)

	_, err = os.Stat(aptRepos)
	assert.True(t, os.IsNotExist(err))

	_, err = os.Stat(aptRepos + ".bak")
	assert.NoError(t, err)
}

func TestResetAptSource_NoBackup(t *testing.T) {
	tempDir := t.TempDir()
	originalAptRepos := aptRepos
	aptRepos = filepath.Join(tempDir, "sources.list")
	defer func() { aptRepos = originalAptRepos }()

	err := resetAptSource()
	assert.NoError(t, err)
}

func TestBackupYumSource_Success(t *testing.T) {
	tempDir := t.TempDir()
	originalYumRepos := yumRepos
	yumRepos = tempDir
	defer func() { yumRepos = originalYumRepos }()

	err := os.Mkdir(filepath.Join(tempDir, "bak"), testDirPermission)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "test.repo"), []byte("test"), testFilePermission)
	assert.NoError(t, err)

	err = backupYumSource()
	assert.NoError(t, err)

	_, err = os.Stat(filepath.Join(tempDir, "test.repo"))
	assert.True(t, os.IsNotExist(err))

	_, err = os.Stat(filepath.Join(tempDir, "bak", "test.repo"))
	assert.NoError(t, err)
}

func TestResetYumSource_Success(t *testing.T) {
	tempDir := t.TempDir()
	originalYumRepos := yumRepos
	yumRepos = tempDir
	defer func() { yumRepos = originalYumRepos }()

	bakDir := filepath.Join(tempDir, "bak")
	err := os.Mkdir(bakDir, testDirPermission)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "bke.repo"), []byte("test"), testFilePermission)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(bakDir, "original.repo"), []byte("test"), testFilePermission)
	assert.NoError(t, err)

	err = resetYumSource()
	assert.NoError(t, err)

	_, err = os.Stat(filepath.Join(tempDir, "bke.repo"))
	assert.True(t, os.IsNotExist(err))

	_, err = os.Stat(bakDir)
	assert.True(t, os.IsNotExist(err))

	_, err = os.Stat(filepath.Join(tempDir, "original.repo"))
	assert.NoError(t, err)
}

func TestResetYumSource_NoBakDir(t *testing.T) {
	tempDir := t.TempDir()
	originalYumRepos := yumRepos
	yumRepos = tempDir
	defer func() { yumRepos = originalYumRepos }()

	err := resetYumSource()
	assert.NoError(t, err)
}

func TestResetYumSource_NoBkeRepo(t *testing.T) {
	tempDir := t.TempDir()
	originalYumRepos := yumRepos
	yumRepos = tempDir
	defer func() { yumRepos = originalYumRepos }()

	bakDir := filepath.Join(tempDir, "bak")
	err := os.Mkdir(bakDir, testDirPermission)
	assert.NoError(t, err)

	err = resetYumSource()
	assert.NoError(t, err)
}

func TestGetRPMDownloadPath(t *testing.T) {
	_, err := GetRPMDownloadPath("http://test.com")
	if err != nil {
		t.Logf("Expected error: %v", err)
	}
}

func TestSetSource(t *testing.T) {
	err := SetSource("http://test.com")
	if err != nil {
		t.Logf("Expected error: %v", err)
	}
}

func TestResetSource(t *testing.T) {
	err := ResetSource()
	if err != nil {
		t.Logf("Expected error: %v", err)
	}
}

func TestBackupYumSourceCreateBak(t *testing.T) {
	tempDir := t.TempDir()
	originalYumRepos := yumRepos
	yumRepos = tempDir
	defer func() { yumRepos = originalYumRepos }()

	err := os.WriteFile(filepath.Join(tempDir, "test.repo"), []byte("test"), testFilePermission)
	assert.NoError(t, err)

	err = backupYumSource()
	assert.NoError(t, err)
}

func TestResetYumSourceWithSubdir(t *testing.T) {
	tempDir := t.TempDir()
	originalYumRepos := yumRepos
	yumRepos = tempDir
	defer func() { yumRepos = originalYumRepos }()

	bakDir := filepath.Join(tempDir, "bak")
	err := os.Mkdir(bakDir, testDirPermission)
	assert.NoError(t, err)

	subDir := filepath.Join(bakDir, "subdir")
	err = os.Mkdir(subDir, testDirPermission)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "bke.repo"), []byte("test"), testFilePermission)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(bakDir, "test.repo"), []byte("test"), testFilePermission)
	assert.NoError(t, err)

	err = resetYumSource()
	assert.NoError(t, err)
}

func TestResetAptSourceWithBackup(t *testing.T) {
	tempDir := t.TempDir()
	originalAptRepos := aptRepos
	aptRepos = filepath.Join(tempDir, "sources.list")
	defer func() { aptRepos = originalAptRepos }()

	err := os.WriteFile(aptRepos, []byte("test"), testFilePermission)
	assert.NoError(t, err)
	err = os.WriteFile(aptRepos+".bak", []byte("backup"), testFilePermission)
	assert.NoError(t, err)

	err = resetAptSource()
	if err != nil {
		t.Logf("Expected error: %v", err)
	}
}

func TestResetYumSourceSkipBkeRepo(t *testing.T) {
	tempDir := t.TempDir()
	originalYumRepos := yumRepos
	yumRepos = tempDir
	defer func() { yumRepos = originalYumRepos }()

	bakDir := filepath.Join(tempDir, "bak")
	err := os.Mkdir(bakDir, testDirPermission)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "bke.repo"), []byte("test"), testFilePermission)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(bakDir, "bke.repo"), []byte("test"), testFilePermission)
	assert.NoError(t, err)

	err = resetYumSource()
	assert.NoError(t, err)
}

func TestResetYumSourceMultipleFiles(t *testing.T) {
	tempDir := t.TempDir()
	originalYumRepos := yumRepos
	yumRepos = tempDir
	defer func() { yumRepos = originalYumRepos }()

	bakDir := filepath.Join(tempDir, "bak")
	err := os.Mkdir(bakDir, testDirPermission)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "bke.repo"), []byte("bke"), testFilePermission)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(bakDir, "test1.repo"), []byte("test1"), testFilePermission)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(bakDir, "test2.repo"), []byte("test2"), testFilePermission)
	assert.NoError(t, err)

	err = resetYumSource()
	assert.NoError(t, err)
}

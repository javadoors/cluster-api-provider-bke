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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
)

const numThirtyTwo = 32

func TestCatAndSearchWithEmptyKeyAndReg(t *testing.T) {
	result, err := catAndSearch("/path/to/file", "", "")

	assert.False(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key or reg at least one is required")
}

func TestCatAndSearchWithNotExistFile(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.IsFile,
		func(string) bool {
			return false
		})

	result, err := catAndSearch("/nonexistent/file", "test", "")

	assert.False(t, result)
	assert.Error(t, err)
}

func TestCatAndSearchWithKeyFound(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	tmpFile, err := os.CreateTemp("", "cat_search_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("line1\ntest keyword\nline3")
	assert.NoError(t, err)
	tmpFile.Close()

	result, err := catAndSearch(tmpFile.Name(), "test keyword", "")

	assert.True(t, result)
	assert.NoError(t, err)
}

func TestCatAndSearchWithKeyNotFound(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cat_search_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("line1\nline2\nline3")
	assert.NoError(t, err)
	tmpFile.Close()

	result, err := catAndSearch(tmpFile.Name(), "nonexistent", "")

	assert.False(t, result)
	assert.NoError(t, err)
}

func TestCatAndSearchWithRegexFound(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cat_search_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("value = 123\nother = 456")
	assert.NoError(t, err)
	tmpFile.Close()

	result, err := catAndSearch(tmpFile.Name(), "", `\d+`)

	assert.True(t, result)
	assert.NoError(t, err)
}

func TestCatAndSearchWithRegexNotFound(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cat_search_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("no numbers here")
	assert.NoError(t, err)
	tmpFile.Close()

	result, err := catAndSearch(tmpFile.Name(), "", `\d+`)

	assert.False(t, result)
	assert.NoError(t, err)
}

func TestCatAndSearchWithInvalidRegex(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cat_search_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("test content")
	assert.NoError(t, err)
	tmpFile.Close()

	result, err := catAndSearch(tmpFile.Name(), "", "[invalid regex")

	assert.False(t, result)
	assert.Error(t, err)
}

func TestCatAndReplaceWithEmptySrcAndReg(t *testing.T) {
	err := catAndReplace("/path/to/file", "", "", "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "src or reg at least one is required")
}

func TestCatAndReplaceWithNotExistFile(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.IsFile,
		func(string) bool {
			return false
		})

	err := catAndReplace("/nonexistent/file", "old", "new", "")

	assert.Error(t, err)
}

func TestCatAndReplaceWithKeyReplacement(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cat_replace_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("hello world\ntest line\nhello again")
	assert.NoError(t, err)
	tmpFile.Close()

	err = catAndReplace(tmpFile.Name(), "hello", "greetings", "")

	assert.NoError(t, err)

	content, err := os.ReadFile(tmpFile.Name())
	assert.NoError(t, err)
	assert.Contains(t, string(content), "greetings world")
	assert.NotContains(t, string(content), "hello world")
}

func TestCatAndReplaceWithNoMatch(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cat_replace_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("no match here")
	assert.NoError(t, err)
	tmpFile.Close()

	err = catAndReplace(tmpFile.Name(), "nonexistent", "replaced", "")

	assert.NoError(t, err)

	content, err := os.ReadFile(tmpFile.Name())
	assert.NoError(t, err)
	assert.Equal(t, "no match here", strings.TrimSpace(string(content)))
}

func TestCatAndReplaceWithRegexReplacement(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cat_replace_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("number1: 123\nnumber2: 456")
	assert.NoError(t, err)
	tmpFile.Close()

	err = catAndReplace(tmpFile.Name(), "", "NUM", `\d+`)

	assert.NoError(t, err)

	content, err := os.ReadFile(tmpFile.Name())
	assert.NoError(t, err)
	assert.Contains(t, string(content), "NUM")
}

func TestCatAndReplaceWithCommentPrefix(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cat_replace_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("swap line\nother line")
	assert.NoError(t, err)
	tmpFile.Close()

	err = catAndReplace(tmpFile.Name(), "", "#", "swap")

	assert.NoError(t, err)

	content, err := os.ReadFile(tmpFile.Name())
	assert.NoError(t, err)
	assert.Contains(t, string(content), "#swap line")
}

func TestCatAndReplaceWithDoubleSlashPrefix(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cat_replace_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("config line\nother line")
	assert.NoError(t, err)
	tmpFile.Close()

	err = catAndReplace(tmpFile.Name(), "", "//", "config")

	assert.NoError(t, err)

	content, err := os.ReadFile(tmpFile.Name())
	assert.NoError(t, err)
	assert.Contains(t, string(content), "//config line")
}

func TestCatAndReplaceWithWriteError(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cat_replace_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("test content")
	assert.NoError(t, err)
	tmpFile.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(os.WriteFile,
		func(string, []byte, os.FileMode) error {
			return assert.AnError
		})

	err = catAndReplace(tmpFile.Name(), "test", "newtest", "")

	assert.Error(t, err)
}

func TestBakFileWithBackupDisabled(t *testing.T) {
	ep := &EnvPlugin{
		backup: "false",
	}

	err := ep.bakFile("/path/to/file")

	assert.NoError(t, err)
}

func TestBakFileWithNotExistFile(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(utils.Exists,
		func(string) bool {
			return false
		})

	ep := &EnvPlugin{
		backup: "true",
	}

	err := ep.bakFile("/nonexistent/file")

	assert.NoError(t, err)
}

func TestBakFileWithSuccess(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "bak_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("original content")
	assert.NoError(t, err)
	tmpFile.Close()

	ep := &EnvPlugin{
		backup: "true",
	}

	err = ep.bakFile(tmpFile.Name())

	assert.NoError(t, err)

	files, err := filepath.Glob(tmpFile.Name() + "-*.bak")
	assert.NoError(t, err)
	assert.NotEmpty(t, files)

	for _, f := range files {
		os.Remove(f)
	}
}

func TestBakFileWithOpenError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bak_test_dir_*.txt")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	ep := &EnvPlugin{
		backup: "true",
	}

	err = ep.bakFile(tmpDir)

	assert.Error(t, err)
}

func TestMd5SumWithOpenError(t *testing.T) {
	result, err := md5Sum("/nonexistent/file")

	assert.Empty(t, result)
	assert.Error(t, err)
}

func TestMd5SumWithValidFile(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "md5_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString("hello world")
	assert.NoError(t, err)
	tmpFile.Close()

	result, err := md5Sum(tmpFile.Name())

	assert.NotEmpty(t, result)
	assert.NoError(t, err)
	assert.Equal(t, numThirtyTwo, len(result))
}

func TestCompareFileMD5WithNotExistFile(t *testing.T) {
	result, err := compareFileMD5("/exist/file", "/nonexistent/file")

	assert.False(t, result)
	assert.NoError(t, err)
}

func TestCompareFileMD5WithIdenticalFiles(t *testing.T) {
	tmpFile1, err := os.CreateTemp("", "compare_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile1.Name())

	tmpFile2, err := os.CreateTemp("", "compare_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile2.Name())

	content := "same content for both files"
	_, err = tmpFile1.WriteString(content)
	assert.NoError(t, err)
	_, err = tmpFile2.WriteString(content)
	assert.NoError(t, err)
	tmpFile1.Close()
	tmpFile2.Close()

	result, err := compareFileMD5(tmpFile1.Name(), tmpFile2.Name())

	assert.True(t, result)
	assert.NoError(t, err)
}

func TestCompareFileMD5WithDifferentFiles(t *testing.T) {
	tmpFile1, err := os.CreateTemp("", "compare_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile1.Name())

	tmpFile2, err := os.CreateTemp("", "compare_test_*.txt")
	assert.NoError(t, err)
	defer os.Remove(tmpFile2.Name())

	_, err = tmpFile1.WriteString("content file 1")
	assert.NoError(t, err)
	_, err = tmpFile2.WriteString("content file 2")
	assert.NoError(t, err)
	tmpFile1.Close()
	tmpFile2.Close()

	result, err := compareFileMD5(tmpFile1.Name(), tmpFile2.Name())

	assert.False(t, result)
	assert.NoError(t, err)
}

func TestCatAndSearchTableDriven(t *testing.T) {
	testCases := []struct {
		name        string
		content     string
		key         string
		regex       string
		expected    bool
		expectError bool
	}{
		{
			name:        "keyFound",
			content:     "line1\ntest key\nline3",
			key:         "test key",
			regex:       "",
			expected:    true,
			expectError: false,
		},
		{
			name:        "keyNotFound",
			content:     "line1\nline2\nline3",
			key:         "nonexistent",
			regex:       "",
			expected:    false,
			expectError: false,
		},
		{
			name:        "regexFound",
			content:     "email: test@example.com",
			key:         "",
			regex:       `[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`,
			expected:    true,
			expectError: false,
		},
		{
			name:        "regexNotFound",
			content:     "no email here",
			key:         "",
			regex:       `[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`,
			expected:    false,
			expectError: false,
		},
		{
			name:        "emptyFile",
			content:     "",
			key:         "test",
			regex:       "",
			expected:    false,
			expectError: false,
		},
		{
			name:        "multilineKey",
			content:     "start\nmiddle\nend",
			key:         "middle",
			regex:       "",
			expected:    true,
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpFile, err := os.CreateTemp("", "table_test_*.txt")
			assert.NoError(t, err)
			defer os.Remove(tmpFile.Name())

			_, err = tmpFile.WriteString(tc.content)
			assert.NoError(t, err)
			tmpFile.Close()

			result, err := catAndSearch(tmpFile.Name(), tc.key, tc.regex)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestCatAndReplaceTableDriven(t *testing.T) {
	testCases := []struct {
		name         string
		content      string
		src          string
		sub          string
		regex        string
		expectChange bool
	}{
		{
			name:         "simpleReplace",
			content:      "hello world",
			src:          "hello",
			sub:          "goodbye",
			regex:        "",
			expectChange: true,
		},
		{
			name:         "noMatch",
			content:      "hello world",
			src:          "nonexistent",
			sub:          "replaced",
			regex:        "",
			expectChange: false,
		},
		{
			name:         "replaceAll",
			content:      "aaa bbb aaa",
			src:          "aaa",
			sub:          "ccc",
			regex:        "",
			expectChange: true,
		},
		{
			name:         "regexReplace",
			content:      "numbers: 123 456",
			src:          "",
			sub:          "NUM",
			regex:        `\d+`,
			expectChange: true,
		},
		{
			name:         "commentOut",
			content:      "swap line",
			src:          "",
			sub:          "#",
			regex:        "swap",
			expectChange: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpFile, err := os.CreateTemp("", "replace_test_*.txt")
			assert.NoError(t, err)
			defer os.Remove(tmpFile.Name())

			_, err = tmpFile.WriteString(tc.content)
			assert.NoError(t, err)
			tmpFile.Close()

			originalContent, _ := os.ReadFile(tmpFile.Name())
			originalTrimmed := strings.TrimSpace(string(originalContent))

			err = catAndReplace(tmpFile.Name(), tc.src, tc.sub, tc.regex)

			assert.NoError(t, err)

			newContent, _ := os.ReadFile(tmpFile.Name())
			newTrimmed := strings.TrimSpace(string(newContent))

			if tc.expectChange {
				assert.NotEqual(t, originalTrimmed, newTrimmed)
			} else {
				assert.Equal(t, originalTrimmed, newTrimmed)
			}
		})
	}
}

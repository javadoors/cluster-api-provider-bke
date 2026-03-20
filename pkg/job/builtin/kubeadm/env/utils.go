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
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils"
)

// catAndSearch returns whether the given string is found in the given file.
// // regular matching has higher priority than direct matching.
// example:
// 1.("/etc/aaa", "a", "") -> true if "a" is found in the given file
// 2.("/etc/aaa", "", ".*swap.*") -> false if ".*swap.*" can't match anything
func catAndSearch(path string, key string, reg string) (bool, error) {
	// check parameters
	if key == "" && reg == "" {
		return false, errors.New("key or reg at least one is required")
	}
	if !utils.IsFile(path) {
		return false, errors.Errorf("%s is not a file", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if reg != "" {
			r, err := regexp.Compile(reg)
			if err != nil {
				return false, err
			}
			if r.MatchString(scanner.Text()) {
				return true, nil
			}
			continue
		}

		if strings.Contains(scanner.Text(), key) {
			return true, nil
		}
	}
	return false, nil
}

// catAndReplace returns the given string or regex replaced in the given file.
// regular matching has higher priority than direct matching.
// example :
// 1.(/etc/aaa, "a", "b" ,"")
// replace "a" with "b" at /etc/aaa file.
// 2.(/etc/aaa, "", "b" ,".*swap.*")
// use regex(".*swap.*") to match /etc/aaa file content and replace match string with "b".
// 3.(/etc/aaa, "", "#" ,".*swap.*")
// use regex(".*swap.*") to match /etc/aaa file content and
// add "#" in front of the line where the matched string is located
func catAndReplace(path string, src string, sub string, reg string) error {
	// check parameters
	if src == "" && reg == "" {
		return errors.New("src or reg at least one is required")
	}
	if !utils.IsFile(path) {
		return errors.Errorf("%s is not a file", path)
	}

	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()

	var outString string
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()
		var newLine string

		// if reg is empty, use src to match and replace
		if reg == "" {
			if strings.Contains(line, src) {
				newLine = strings.Replace(line, src, sub, -1)
			} else {
				newLine = line
			}
			outString += newLine + "\n"
			continue
		}

		// if reg is not empty, use regex to match and replace
		r, err := regexp.Compile(reg)
		if err != nil {
			return err
		}
		if !r.MatchString(line) {
			outString += line + "\n"
			continue
		}

		// matched, do replacement
		switch sub {
		case "#":
			newLine = r.ReplaceAllString(line, "#"+line)
		case "//":
			newLine = r.ReplaceAllString(line, "//"+line)
		default:
			newLine = r.ReplaceAllString(line, sub)
		}
		outString += newLine + "\n"
	}

	if err = os.WriteFile(path, []byte(outString), RwRR); err != nil {
		return err
	}
	return nil
}

// bakFile backup the given file
// if the given file is not exist, return nil
func (ep *EnvPlugin) bakFile(path string) error {
	if ep.backup != "true" {
		return nil
	}
	if !utils.Exists(path) {
		return nil
	}

	// use scanner backup
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()

	// out file name have create time
	bakName := fmt.Sprintf("%s-%s.bak", path, time.Now().Format("0601021504"))
	out, err := os.OpenFile(bakName, os.O_CREATE|os.O_RDWR, RwRR)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := bufio.NewReader(in).WriteTo(out); err != nil {
		if err := os.Remove(path + ".bak"); err != nil {
			return errors.Wrap(err, "remove backup file failed")
		}
		return err
	}

	return nil
}

// md5Sum returns the md5 sum of the given file
func md5Sum(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func compareFileMD5(file1 string, file2 string) (bool, error) {
	if !utils.Exists(file2) {
		return false, nil
	}
	md51, err := md5Sum(file1)
	if err != nil {
		return false, err
	}
	md52, err := md5Sum(file2)
	if err != nil {
		return false, err
	}
	return md51 == md52, nil
}

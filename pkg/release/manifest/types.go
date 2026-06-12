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
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	apiv1 "gopkg.openfuyao.cn/cluster-api-provider-bke/api/v1alpha1"
)

const (
	SourceMemory = "Memory"
	SourceDisk   = "Disk"
	SourceOCI    = "OCI"
)

type ReleaseRef struct {
	Version            string
	OCIRef             string
	Digest             string
	VerifySignature    bool
	SignatureKey       string
	AllowCacheFallback bool
}

func (r ReleaseRef) CacheKey() string {
	if r.Digest != "" {
		return sanitizeKey(r.Digest)
	}
	if r.Version != "" {
		return sanitizeKey(r.Version)
	}
	sum := sha256.Sum256([]byte(r.OCIRef))
	return fmt.Sprintf("%x", sum[:])
}

type Bundle struct {
	Release    apiv1.ReleaseImage
	Components map[string]apiv1.ComponentVersion
	// Files holds all YAML paths from the release OCI artifact (release.yaml, component.yaml, resource manifests).
	Files         map[string][]byte
	Digest        string
	Source        string
	CacheFallback bool
}

func (b *Bundle) DeepCopy() *Bundle {
	if b == nil {
		return nil
	}
	out := &Bundle{
		Release:       *b.Release.DeepCopy(),
		Digest:        b.Digest,
		Source:        b.Source,
		CacheFallback: b.CacheFallback,
		Components:    make(map[string]apiv1.ComponentVersion, len(b.Components)),
	}
	for k, v := range b.Components {
		out.Components[k] = *v.DeepCopy()
	}
	if len(b.Files) > 0 {
		out.Files = make(map[string][]byte, len(b.Files))
		for k, v := range b.Files {
			dup := make([]byte, len(v))
			copy(dup, v)
			out.Files[k] = dup
		}
	}
	return out
}

type BundleFiles struct {
	Files map[string][]byte
}

func ComponentKey(name, version string) string {
	return strings.TrimSpace(name) + "@" + strings.TrimSpace(version)
}

func sanitizeKey(key string) string {
	key = strings.TrimSpace(key)
	key = strings.ReplaceAll(key, ":", "-")
	key = strings.ReplaceAll(key, "/", "-")
	key = strings.ReplaceAll(key, "\\", "-")
	return key
}

func SortedFileNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

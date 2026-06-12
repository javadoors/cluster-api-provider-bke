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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type cacheMetadata struct {
	Version  string    `json:"version"`
	OCIRef   string    `json:"ociRef"`
	Digest   string    `json:"digest"`
	Verified bool      `json:"verified"`
	CachedAt time.Time `json:"cachedAt"`
}

func removeDiskCacheDir(diskRoot, cacheKey string) error {
	dir := filepath.Join(diskRoot, cacheKey)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(dir)
}

func (s *Store) writeDiskCache(ref ReleaseRef, files *BundleFiles, digest string) error {
	if s.diskRoot == "" || files == nil {
		return nil
	}
	dir := filepath.Join(s.diskRoot, ref.CacheKey())
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	for name, data := range files.Files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0640); err != nil {
			return err
		}
	}
	meta := cacheMetadata{
		Version:  ref.Version,
		OCIRef:   ref.OCIRef,
		Digest:   digest,
		Verified: ref.VerifySignature,
		CachedAt: time.Now(),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0640)
}

func (s *Store) loadDiskCache(ref ReleaseRef) (*Bundle, error) {
	if s.diskRoot == "" {
		return nil, fmt.Errorf("disk cache root is empty")
	}
	dir := filepath.Join(s.diskRoot, ref.CacheKey())
	files, err := ReadBundleDir(dir)
	if err != nil {
		return nil, err
	}
	meta, err := readCacheMetadata(dir)
	if err != nil {
		return nil, err
	}
	if ref.Digest != "" && meta.Digest != ref.Digest {
		return nil, fmt.Errorf("cache digest mismatch: want %s got %s", ref.Digest, meta.Digest)
	}
	if ref.VerifySignature && !meta.Verified {
		return nil, fmt.Errorf("cache was not signature verified")
	}
	bundle, err := ParseBundle(files)
	if err != nil {
		return nil, err
	}
	bundle.Digest = meta.Digest
	bundle.Source = SourceDisk
	bundle.CacheFallback = true
	return bundle, nil
}

func readCacheMetadata(dir string) (*cacheMetadata, error) {
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil, err
	}
	meta := &cacheMetadata{}
	if err := json.Unmarshal(data, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

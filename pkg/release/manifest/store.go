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
	"context"
	"fmt"
	"sync"
)

type Store struct {
	memory   sync.Map
	diskRoot string
	puller   Puller
	verifier Verifier
}

func NewStore(diskRoot string, puller Puller, verifier Verifier) *Store {
	if puller == nil {
		puller = OCIPuller{}
	}
	if verifier == nil {
		verifier = NoopVerifier{}
	}
	return &Store{diskRoot: diskRoot, puller: puller, verifier: verifier}
}

// ResolveRelease returns a validated release bundle from memory or disk cache.
// It never pulls from OCI; callers that need a fresh pull must use RefreshRelease
// and CommitRelease after validation (ReleaseImageReconciler).
func (s *Store) ResolveRelease(ctx context.Context, ref ReleaseRef) (*Bundle, error) {
	_ = ctx
	key := ref.CacheKey()
	if v, ok := s.memory.Load(key); ok {
		bundle := v.(*Bundle).DeepCopy()
		bundle.Source = SourceMemory
		return bundle, nil
	}

	cached, err := s.loadDiskCache(ref)
	if err != nil {
		return nil, fmt.Errorf("release bundle not cached for %q: %w", key, err)
	}
	s.memory.Store(key, cached.DeepCopy())
	return cached, nil
}

// RefreshRelease always pulls the release artifact from OCI (or local ref), verifies
// digest/signature, and parses the bundle without reading or writing the cache.
func (s *Store) RefreshRelease(ctx context.Context, ref ReleaseRef) (*Bundle, *BundleFiles, error) {
	bundle, files, err := s.resolveFromPuller(ctx, ref)
	if err == nil {
		return bundle, files, nil
	}
	if !ref.AllowCacheFallback {
		return nil, nil, err
	}
	cached, cacheErr := s.loadDiskCache(ref)
	if cacheErr != nil {
		return nil, nil, fmt.Errorf("refresh release failed: pull=%v; cache=%v", err, cacheErr)
	}
	files = &BundleFiles{Files: cached.Files}
	return cached, files, nil
}

// CommitRelease persists a validated bundle to memory and disk cache.
func (s *Store) CommitRelease(ref ReleaseRef, bundle *Bundle, files *BundleFiles) error {
	if bundle == nil {
		return fmt.Errorf("bundle is nil")
	}
	key := ref.CacheKey()
	s.memory.Store(key, bundle.DeepCopy())
	if files == nil && len(bundle.Files) > 0 {
		files = &BundleFiles{Files: bundle.Files}
	}
	return s.writeDiskCache(ref, files, bundle.Digest)
}

// EvictRelease removes the cached bundle (memory and on-disk yaml/file artifacts).
func (s *Store) EvictRelease(ref ReleaseRef) error {
	key := ref.CacheKey()
	s.memory.Delete(key)
	if s.diskRoot == "" {
		return nil
	}
	return removeDiskCacheDir(s.diskRoot, key)
}

func (s *Store) resolveFromPuller(ctx context.Context, ref ReleaseRef) (*Bundle, *BundleFiles, error) {
	files, digest, err := s.puller.Pull(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	if ref.Digest != "" && digest != ref.Digest {
		return nil, nil, fmt.Errorf("digest mismatch: want %s got %s", ref.Digest, digest)
	}
	if err := s.verifier.Verify(ctx, ref, digest, files); err != nil {
		return nil, nil, err
	}
	bundle, err := ParseBundle(files)
	if err != nil {
		return nil, nil, err
	}
	bundle.Digest = digest
	bundle.Source = SourceOCI
	return bundle, files, nil
}

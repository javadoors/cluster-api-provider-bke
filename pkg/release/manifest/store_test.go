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
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePuller struct {
	files  *BundleFiles
	digest string
	err    error
	calls  int
}

func (f *fakePuller) Pull(_ context.Context, _ ReleaseRef) (*BundleFiles, string, error) {
	f.calls++
	if f.err != nil {
		return nil, "", f.err
	}
	return f.files, f.digest, nil
}

func TestStoreResolveReleaseUsesMemoryCache(t *testing.T) {
	files := testBundleFiles("v26.03", "v1.29.1-of.1", "v3.5.21-of.1")
	digest := DigestFiles(files)
	puller := &fakePuller{files: files, digest: digest}
	store := NewStore(t.TempDir(), puller, nil)

	ref := ReleaseRef{Version: "v26.03", OCIRef: "file://release", Digest: digest}
	bundle, refreshFiles, err := store.RefreshRelease(context.Background(), ref)
	require.NoError(t, err)
	require.NoError(t, store.CommitRelease(ref, bundle, refreshFiles))

	first, err := store.ResolveRelease(context.Background(), ref)
	require.NoError(t, err)
	second, err := store.ResolveRelease(context.Background(), ref)
	require.NoError(t, err)

	assert.Equal(t, SourceMemory, first.Source)
	assert.Equal(t, SourceMemory, second.Source)
	assert.Equal(t, 1, puller.calls)
}

func TestStoreResolveReleaseLoadsDiskCache(t *testing.T) {
	files := testBundleFiles("v26.03", "v1.29.1-of.1", "v3.5.21-of.1")
	digest := DigestFiles(files)
	diskRoot := t.TempDir()
	writeCacheForTest(t, diskRoot, "v26.03", digest, files)
	store := NewStore(diskRoot, &fakePuller{err: errors.New("network unavailable")}, nil)

	bundle, err := store.ResolveRelease(context.Background(), ReleaseRef{
		Version:            "v26.03",
		OCIRef:             "registry/release:v26.03",
		Digest:             digest,
		AllowCacheFallback: true,
	})

	require.NoError(t, err)
	assert.Equal(t, SourceDisk, bundle.Source)
	assert.True(t, bundle.CacheFallback)
	assert.Equal(t, digest, bundle.Digest)
	assert.Equal(t, 0, store.pullerCalls())
}

func (s *Store) pullerCalls() int {
	if p, ok := s.puller.(*fakePuller); ok {
		return p.calls
	}
	return -1
}

func TestStoreResolveReleaseErrorsWhenCacheMissing(t *testing.T) {
	store := NewStore(t.TempDir(), &fakePuller{}, nil)

	_, err := store.ResolveRelease(context.Background(), ReleaseRef{
		Version: "v26.03",
		OCIRef:  "registry/release:v26.03",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not cached")
}

func TestStoreRefreshReleaseRejectsDigestMismatch(t *testing.T) {
	files := testBundleFiles("v26.03", "v1.29.1-of.1", "v3.5.21-of.1")
	store := NewStore(t.TempDir(), &fakePuller{files: files, digest: DigestFiles(files)}, nil)

	_, _, err := store.RefreshRelease(context.Background(), ReleaseRef{
		Version: "v26.03",
		OCIRef:  "registry/release:v26.03",
		Digest:  "sha256:bad",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "digest mismatch")
}

func TestStoreRefreshReleaseAlwaysPulls(t *testing.T) {
	files := testBundleFiles("v26.03", "v1.29.1-of.1", "v3.5.21-of.1")
	digest := DigestFiles(files)
	puller := &fakePuller{files: files, digest: digest}
	store := NewStore(t.TempDir(), puller, nil)
	ref := ReleaseRef{Version: "v26.03", OCIRef: "registry/release:v26.03", Digest: digest}

	_, _, err := store.RefreshRelease(context.Background(), ref)
	require.NoError(t, err)
	_, _, err = store.RefreshRelease(context.Background(), ref)
	require.NoError(t, err)

	assert.Equal(t, 2, puller.calls)
}

func TestStoreRefreshReleaseFallsBackToDiskCache(t *testing.T) {
	files := testBundleFiles("v26.03", "v1.29.1-of.1", "v3.5.21-of.1")
	digest := DigestFiles(files)
	diskRoot := t.TempDir()
	writeCacheForTest(t, diskRoot, "v26.03", digest, files)
	store := NewStore(diskRoot, &fakePuller{err: errors.New("network unavailable")}, nil)

	bundle, _, err := store.RefreshRelease(context.Background(), ReleaseRef{
		Version:            "v26.03",
		OCIRef:             "registry/release:v26.03",
		Digest:             digest,
		AllowCacheFallback: true,
	})

	require.NoError(t, err)
	assert.Equal(t, SourceDisk, bundle.Source)
	assert.True(t, bundle.CacheFallback)
}

func TestStoreFailedRefreshDoesNotOverwriteCommittedCache(t *testing.T) {
	goodFiles := testBundleFiles("v26.03", "v1.29.1-of.1", "v3.5.21-of.1")
	goodDigest := DigestFiles(goodFiles)
	badFiles := testBundleFiles("v26.03", "v1.29.1-of.1", "v3.4.0")
	badDigest := DigestFiles(badFiles)

	puller := &fakePuller{files: goodFiles, digest: goodDigest}
	store := NewStore(t.TempDir(), puller, nil)
	ref := ReleaseRef{Version: "v26.03", OCIRef: "registry/release:v26.03", Digest: goodDigest}

	goodBundle, goodBundleFiles, err := store.RefreshRelease(context.Background(), ref)
	require.NoError(t, err)
	require.NoError(t, store.CommitRelease(ref, goodBundle, goodBundleFiles))

	puller.files = badFiles
	puller.digest = badDigest
	_, _, err = store.RefreshRelease(context.Background(), ref)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "digest mismatch")

	cached, err := store.ResolveRelease(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, goodDigest, cached.Digest)
	assert.Equal(t, badDigest, DigestFiles(&BundleFiles{Files: badFiles.Files}))
}

func TestStoreEvictReleaseRemovesMemoryAndDisk(t *testing.T) {
	files := testBundleFiles("v26.03", "v1.29.1-of.1", "v3.5.21-of.1")
	digest := DigestFiles(files)
	diskRoot := t.TempDir()
	store := NewStore(diskRoot, &fakePuller{files: files, digest: digest}, nil)
	ref := ReleaseRef{Version: "v26.03", OCIRef: "registry/release:v26.03", Digest: digest}

	bundle, bundleFiles, err := store.RefreshRelease(context.Background(), ref)
	require.NoError(t, err)
	require.NoError(t, store.CommitRelease(ref, bundle, bundleFiles))

	_, err = store.ResolveRelease(context.Background(), ref)
	require.NoError(t, err)

	require.NoError(t, store.EvictRelease(ref))
	_, err = store.ResolveRelease(context.Background(), ref)
	require.Error(t, err)

	cacheDir := filepath.Join(diskRoot, ref.CacheKey())
	_, err = os.Stat(cacheDir)
	require.True(t, os.IsNotExist(err))
}

func TestFilePullerReadsLocalBundle(t *testing.T) {
	files := testBundleFiles("v26.03", "v1.29.1-of.1", "v3.5.21-of.1")
	dir := t.TempDir()
	writeBundleFiles(t, dir, files)

	got, digest, err := FilePuller{}.Pull(context.Background(), ReleaseRef{OCIRef: "file://" + dir})

	require.NoError(t, err)
	assert.Equal(t, DigestFiles(files), digest)
	assert.Equal(t, files.Files["release.yaml"], got.Files["release.yaml"])
}

func TestOCIPullerUsesFilePullerForLocalRefs(t *testing.T) {
	files := testBundleFiles("v26.03", "v1.29.1-of.1", "v3.5.21-of.1")
	dir := t.TempDir()
	writeBundleFiles(t, dir, files)

	got, digest, err := OCIPuller{}.Pull(context.Background(), ReleaseRef{OCIRef: dir})

	require.NoError(t, err)
	assert.Equal(t, DigestFiles(files), digest)
	assert.Equal(t, files.Files["components/etcd/component.yaml"], got.Files["components/etcd/component.yaml"])
}

func TestNoopVerifierRequiresSignatureKey(t *testing.T) {
	err := NoopVerifier{}.Verify(context.Background(), ReleaseRef{VerifySignature: true}, "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature key")

	require.NoError(t, NoopVerifier{}.Verify(context.Background(), ReleaseRef{VerifySignature: true, SignatureKey: "key"}, "", nil))
}

func TestReleaseRefCacheKeyFallbacks(t *testing.T) {
	assert.Equal(t, "sha256-abc", ReleaseRef{Digest: "sha256:abc", Version: "v1", OCIRef: "repo:v1"}.CacheKey())
	assert.Equal(t, "v1.0.0", ReleaseRef{Version: "v1.0.0", OCIRef: "repo:v1"}.CacheKey())
	assert.NotEmpty(t, ReleaseRef{OCIRef: "repo:v1"}.CacheKey())
}

func TestReadBundleDirRejectsEmptyAndFile(t *testing.T) {
	_, err := ReadBundleDir(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no yaml files")

	file := filepath.Join(t.TempDir(), "release.yaml")
	require.NoError(t, os.WriteFile(file, []byte("kind: ReleaseImage\n"), 0640))
	_, err = ReadBundleDir(file)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
}

func testBundleFiles(releaseVersion, k8sVersion, etcdVersion string) *BundleFiles {
	return &BundleFiles{Files: map[string][]byte{
		"release.yaml": []byte(`apiVersion: bke.bocloud.com/v1beta1
kind: ReleaseImage
metadata:
  name: test-release
spec:
  version: ` + releaseVersion + `
  ociRef: registry/release:` + releaseVersion + `
  upgrade:
    components:
    - name: kubernetes
      version: ` + k8sVersion + `
    - name: etcd
      version: ` + etcdVersion + `
`),
		"components/kubernetes/component.yaml": []byte(`apiVersion: bke.bocloud.com/v1beta1
kind: ComponentVersion
metadata:
  name: kubernetes
spec:
  name: kubernetes
  version: ` + k8sVersion + `
  type: binary
  compatibility:
    constraints:
    - component: etcd
      rule: ">=3.5.10 <3.6.0"
`),
		"components/etcd/component.yaml": []byte(`apiVersion: bke.bocloud.com/v1beta1
kind: ComponentVersion
metadata:
  name: etcd
spec:
  name: etcd
  version: ` + etcdVersion + `
  type: binary
`),
	}}
}

func writeBundleFiles(t *testing.T, root string, files *BundleFiles) {
	t.Helper()
	for name, data := range files.Files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
		require.NoError(t, os.WriteFile(path, data, 0640))
	}
}

func writeCacheForTest(t *testing.T, diskRoot, version, digest string, files *BundleFiles) {
	t.Helper()
	dir := filepath.Join(diskRoot, sanitizeKey(digest))
	require.NoError(t, os.MkdirAll(dir, 0750))
	for name, data := range files.Files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
		require.NoError(t, os.WriteFile(path, data, 0640))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{
  "version": "`+version+`",
  "ociRef": "registry/release:`+version+`",
  "digest": "`+digest+`",
  "verified": false,
  "cachedAt": "2026-05-19T00:00:00Z"
}`), 0640))
}

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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/pkg/oci"
)

type Puller interface {
	Pull(ctx context.Context, ref ReleaseRef) (*BundleFiles, string, error)
}

type Verifier interface {
	Verify(ctx context.Context, ref ReleaseRef, digest string, files *BundleFiles) error
}

type NoopVerifier struct{}

func (NoopVerifier) Verify(_ context.Context, ref ReleaseRef, _ string, _ *BundleFiles) error {
	if ref.VerifySignature && ref.SignatureKey == "" {
		return fmt.Errorf("signature key is required when verifySignature=true")
	}
	return nil
}

type FilePuller struct{}

func (FilePuller) Pull(_ context.Context, ref ReleaseRef) (*BundleFiles, string, error) {
	path := strings.TrimPrefix(ref.OCIRef, "file://")
	if path == "" {
		return nil, "", fmt.Errorf("ociRef is empty")
	}
	files, err := ReadBundleDir(path)
	if err != nil {
		return nil, "", err
	}
	return files, DigestFiles(files), nil
}

type OCIPuller struct {
	Client *oci.Client
}

func (p OCIPuller) Pull(ctx context.Context, ref ReleaseRef) (*BundleFiles, string, error) {
	if isLocalRef(ref.OCIRef) {
		return FilePuller{}.Pull(ctx, ref)
	}

	imageRef := ref.OCIRef
	if imageRef == "" {
		return nil, "", fmt.Errorf("ociRef is empty")
	}
	if ref.Digest != "" && !strings.Contains(imageRef, "@") {
		imageRef = imageRef + "@" + ref.Digest
	}

	if p.Client == nil {
		return nil, "", fmt.Errorf("OCIPuller.Client is required")
	}
	client := p.Client
	image, err := client.Pull(ctx, imageRef)
	if err != nil {
		return nil, "", fmt.Errorf("pull release image %s failed: %w", imageRef, err)
	}

	layers, err := image.GetFilesByExtensions(".yaml", ".yml")
	if err != nil {
		return nil, "", err
	}
	files := &BundleFiles{Files: make(map[string][]byte, len(layers))}
	for _, layer := range layers {
		files.Files[layer.Path] = layer.Content
	}
	if len(files.Files) == 0 {
		return nil, "", fmt.Errorf("no yaml files found in release image %s", imageRef)
	}
	return files, DigestFiles(files), nil
}

func ReadBundleDir(root string) (*BundleFiles, error) {
	stat, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	files := map[string][]byte{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".yaml") && !strings.HasSuffix(d.Name(), ".yml") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no yaml files found in %s", root)
	}
	return &BundleFiles{Files: files}, nil
}

func DigestFiles(files *BundleFiles) string {
	h := sha256.New()
	for _, name := range SortedFileNames(files.Files) {
		if _, err := h.Write([]byte(name)); err != nil {
			panic(fmt.Errorf("write release file name digest: %w", err))
		}
		if _, err := h.Write([]byte{0}); err != nil {
			panic(fmt.Errorf("write release digest separator: %w", err))
		}
		if _, err := h.Write(files.Files[name]); err != nil {
			panic(fmt.Errorf("write release file content digest: %w", err))
		}
		if _, err := h.Write([]byte{0}); err != nil {
			panic(fmt.Errorf("write release digest separator: %w", err))
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func isLocalRef(ref string) bool {
	return strings.HasPrefix(ref, "file://") || strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "/") || filepath.IsAbs(ref)
}

/*
 *
 * Copyright (c) 2025 Bocloud Technologies Co., Ltd.
 * installer is licensed under Mulan PSL v2.
 * You can use this software according to the terms and conditions of the Mulan PSL v2.
 * You may obtain a copy of Mulan PSL v2 at:
 *          http://license.coscl.org.cn/MulanPSL2
 * THIS SOFTWARE IS PROVIDED ON AN "AS IS" BASIS, WITHOUT WARRANTIES OF ANY KIND,
 * EITHER EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO NON-INFRINGEMENT,
 * MERCHANTABILITY OR FIT FOR A PARTICULAR PURPOSE.
 * See the Mulan PSL v2 for more details.
 *
 */
package security

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sync"

	"gopkg.openfuyao.cn/cluster-api-provider-bke/utils/log"
)

const (
	// legacySignature is kept solely for decrypting existing BKENode passwords
	// that were encrypted with the old hardcoded key. It must never be used for
	// new encryption.
	legacySignature = "c02ba0b582501608"

	// envAESKey is the environment variable name for injecting the AES key.
	// The value must be a base64-encoded 32-byte random key (AES-256).
	envAESKey = "BKE_PASSWORD_ENCRYPTION_KEY"

	ivLen = aes.BlockSize // 16 bytes

	// magicPrefix is a 4-byte marker prepended to new-format ciphertext so
	// AesDecrypt can deterministically distinguish new format
	// (base64(magic || IV || ciphertext)) from legacy format
	// (base64(ciphertext) with IV = legacy key).
	magicPrefix = "OFMK"
)

var (
	currentKey []byte
	keyOnce    sync.Once
	keyLoadErr error
)

// loadKey lazily loads the AES key from the BKE_PASSWORD_ENCRYPTION_KEY
// environment variable (base64-encoded 32 bytes). When the variable is not
// set it falls back to the legacy key so existing BKENode passwords can still
// be decrypted, but new encryption is refused.
//
// The key is loaded exactly once per process via sync.Once. This is
// intentional: the key is injected as a K8s Secret env var, so a pod
// restart is required to pick up a new key. Runtime key rotation without
// restart is not supported.
func loadKey() ([]byte, error) {
	keyOnce.Do(func() {
		if raw := os.Getenv(envAESKey); raw != "" {
			decoded, err := base64.StdEncoding.DecodeString(raw)
			if err != nil {
				keyLoadErr = fmt.Errorf("invalid %s: base64 decode failed: %w", envAESKey, err)
				log.Errorf("failed to load AES key from env %s: base64 decode error: %v", envAESKey, err)
				return
			}
			if len(decoded) != 32 {
				keyLoadErr = fmt.Errorf("invalid %s: expected 32 bytes, got %d", envAESKey, len(decoded))
				log.Errorf("failed to load AES key from env %s: expected 32 bytes, got %d", envAESKey, len(decoded))
				return
			}
			currentKey = decoded
			log.Infof("AES encryption key loaded from environment variable %s", envAESKey)
			return
		}
		// Env not configured: use legacy key (decrypt-only compatibility).
		log.Warnf("env %s not set, falling back to legacy key (decrypt-only, new encryption will be refused)", envAESKey)
		currentKey = []byte(legacySignature)
	})
	return currentKey, keyLoadErr
}

// isLegacyKey reports whether the currently loaded key is the legacy key.
// When true, new encryption is refused to prevent reusing the compromised key.
func isLegacyKey() bool {
	return string(currentKey) == legacySignature
}

// AesEncrypt encrypts a password using AES-256-CBC with a random IV.
// The ciphertext format is base64(magic || IV || ciphertext).
func AesEncrypt(password string) (string, error) {
	k, err := loadKey()
	if err != nil {
		log.Errorf("AesEncrypt: key load failed: %v", err)
		return "", err
	}
	if isLegacyKey() {
		log.Errorf("AesEncrypt: refusing to encrypt with legacy key, env %s must be set", envAESKey)
		return "", fmt.Errorf("must set %s env before encrypting new passwords", envAESKey)
	}

	block, err := aes.NewCipher(k)
	if err != nil {
		log.Errorf("AesEncrypt: create cipher failed: %v", err)
		return "", err
	}
	blockSize := block.BlockSize()

	iv := make([]byte, ivLen)
	if _, err := rand.Read(iv); err != nil {
		log.Errorf("AesEncrypt: generate IV failed: %v", err)
		return "", err
	}

	padded := pkcs7Padding([]byte(password), blockSize)
	crypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(crypted, padded)

	// Prepend magic prefix so decrypt can distinguish new from legacy format.
	output := append([]byte(magicPrefix), iv...)
	output = append(output, crypted...)
	encoded := base64.StdEncoding.EncodeToString(output)
	log.Info("AesEncrypt: password encrypted successfully with new format")
	return encoded, nil
}

// AesDecrypt decrypts a password, automatically distinguishing the new format
// (base64(magic || IV || ciphertext)) from the legacy format (base64(ciphertext)
// with IV = legacy key). The magic prefix makes detection deterministic so
// there is no risk of misidentifying legacy ciphertext as new format.
// This ensures existing BKENode passwords remain readable after upgrade.
func AesDecrypt(cryptedPassword string) (string, error) {
	// loadKey is called lazily inside the new-format branch below. The
	// legacy branch uses legacySignature directly, so no key load is
	// needed there.

	data, err := base64.StdEncoding.DecodeString(cryptedPassword)
	if err != nil {
		log.Errorf("AesDecrypt: base64 decode failed: %v", err)
		return "", err
	}

	// New format: magic || IV || ciphertext.
	if bytes.HasPrefix(data, []byte(magicPrefix)) {
		k, kErr := loadKey()
		if kErr != nil {
			log.Errorf("AesDecrypt: new-format ciphertext but key load failed: %v", kErr)
			return "", fmt.Errorf("decrypt new-format ciphertext: %w", kErr)
		}
		if isLegacyKey() {
			log.Error("AesDecrypt: new-format ciphertext found but env key not set, cannot decrypt")
			return "", fmt.Errorf("new-format ciphertext requires %s env to be set", envAESKey)
		}

		remainder := data[len(magicPrefix):]
		if len(remainder) <= ivLen || len(remainder)%ivLen != 0 {
			log.Errorf("AesDecrypt: invalid new-format ciphertext length: %d", len(remainder))
			return "", errors.New("invalid new-format ciphertext length")
		}

		block, err := aes.NewCipher(k)
		if err != nil {
			log.Errorf("AesDecrypt: create cipher failed: %v", err)
			return "", err
		}
		iv := remainder[:ivLen]
		crypted := remainder[ivLen:]
		origData := make([]byte, len(crypted))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(origData, crypted)
		result, err := pkcs7UnPaddingSafe(origData)
		if err != nil {
			log.Errorf("AesDecrypt: unpadding new-format ciphertext failed: %v", err)
			return "", err
		}
		log.Info("AesDecrypt: password decrypted successfully (new format)")
		return string(result), nil
	}

	// Legacy format: ciphertext only, IV = legacy key[:blockSize].
	log.Info("AesDecrypt: attempting legacy format decryption")
	if len(data) == 0 || len(data)%ivLen != 0 {
		log.Errorf("AesDecrypt: invalid legacy ciphertext length: %d", len(data))
		return "", errors.New("invalid ciphertext length")
	}
	legacyKey := []byte(legacySignature)
	legacyBlock, err := aes.NewCipher(legacyKey)
	if err != nil {
		log.Errorf("AesDecrypt: create legacy cipher failed: %v", err)
		return "", err
	}
	origData := make([]byte, len(data))
	cipher.NewCBCDecrypter(legacyBlock, legacyKey[:ivLen]).CryptBlocks(origData, data)
	result, err := pkcs7UnPaddingSafe(origData)
	if err != nil {
		log.Errorf("AesDecrypt: unpadding legacy ciphertext failed: %v", err)
		return "", err
	}
	log.Info("AesDecrypt: password decrypted successfully (legacy format)")
	return string(result), nil
}

// pkcs7UnPaddingSafe removes PKCS#7 padding with bounds checking.
func pkcs7UnPaddingSafe(originData []byte) ([]byte, error) {
	length := len(originData)
	if length == 0 {
		return nil, errors.New("empty data")
	}
	unpadding := int(originData[length-1])
	if unpadding > length || unpadding == 0 || unpadding > aes.BlockSize {
		return nil, errors.New("invalid padding")
	}
	return originData[:length-unpadding], nil
}

// pkcs7Padding applies PKCS#7 padding.
func pkcs7Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

// pkcs7UnPadding removes PKCS#7 padding without bounds checking.
// Retained for backward compatibility with any external callers.
func pkcs7UnPadding(originData []byte) []byte {
	length := len(originData)
	unpadding := int(originData[length-1])
	return originData[:(length - unpadding)]
}

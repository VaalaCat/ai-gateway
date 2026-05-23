// Package byokcrypto 提供 BYOK key 的 AES-256-GCM 加解密。
// KEK 优先来自显式 master.byok_kek；缺省 HKDF-SHA256 从 jwt_secret 衍生。
package byokcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// Cipher 持有派生后的 AES-GCM AEAD；线程安全。
type Cipher struct {
	aead cipher.AEAD
}

// NewFromConfig 优先用 byokKEKBase64（base64 32B），缺失则 HKDF 衍生 jwtSecret。
// 二者都为空返 error。
func NewFromConfig(byokKEKBase64, jwtSecret string) (*Cipher, error) {
	var key []byte
	if byokKEKBase64 != "" {
		k, err := base64.StdEncoding.DecodeString(byokKEKBase64)
		if err != nil {
			return nil, fmt.Errorf("decode byok_kek: %w", err)
		}
		if len(k) != 32 {
			return nil, fmt.Errorf("byok_kek must be 32 bytes after base64 decode, got %d", len(k))
		}
		key = k
	} else if jwtSecret != "" {
		k := make([]byte, 32)
		r := hkdf.New(sha256.New, []byte(jwtSecret), nil, []byte("byok-kek-v1"))
		if _, err := io.ReadFull(r, k); err != nil {
			return nil, fmt.Errorf("hkdf derive: %w", err)
		}
		key = k
	} else {
		return nil, errors.New("byokcrypto: both byok_kek and jwt_secret are empty")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// aadFor returns 8-byte LE encoding of ownerID for AEAD AAD.
func aadFor(ownerID uint) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(ownerID))
	return b
}

// Seal 把 plaintext 加密：返回布局 nonce(12) ‖ ct ‖ tag(16)。
func (c *Cipher) Seal(plaintext string, ownerID uint) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), aadFor(ownerID))
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// Open 验签解密；AAD 不符或损坏返 error。
func (c *Cipher) Open(ciphertext []byte, ownerID uint) (string, error) {
	nsize := c.aead.NonceSize()
	if len(ciphertext) < nsize+c.aead.Overhead() {
		return "", errors.New("byokcrypto: ciphertext too short")
	}
	nonce := ciphertext[:nsize]
	ct := ciphertext[nsize:]
	pt, err := c.aead.Open(nil, nonce, ct, aadFor(ownerID))
	if err != nil {
		return "", fmt.Errorf("byokcrypto: open: %w", err)
	}
	return string(pt), nil
}

// Last4 取 plaintext 末 4 rune；<4 时全返。多字节 unicode 安全。
func Last4(plaintext string) string {
	if plaintext == "" {
		return ""
	}
	r := []rune(plaintext)
	if len(r) <= 4 {
		return string(r)
	}
	return string(r[len(r)-4:])
}

// String 防 fmt %v 意外打印密钥句柄。
func (c *Cipher) String() string { return "[REDACTED]" }

package master

import (
	"encoding/base64"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
)

// TestBYOKCipher_InitFromJWTSecret 验证缺省 byok_kek 时走 HKDF 从 jwt_secret 衍生 KEK。
func TestBYOKCipher_InitFromJWTSecret(t *testing.T) {
	c, err := byokcrypto.NewFromConfig("", "test-jwt-secret-long-enough-32bytes!!")
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("cipher nil")
	}
}

// TestBYOKCipher_InitFromExplicitKEK 验证显式 byok_kek（base64 32B）路径正常。
func TestBYOKCipher_InitFromExplicitKEK(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := byokcrypto.NewFromConfig(base64.StdEncoding.EncodeToString(key), "")
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("cipher nil")
	}
}

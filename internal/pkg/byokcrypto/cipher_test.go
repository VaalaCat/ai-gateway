package byokcrypto

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func makeTestCipher(t *testing.T) *Cipher {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	c, err := NewFromConfig(base64.StdEncoding.EncodeToString(key), "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSealOpen_RoundTrip(t *testing.T) {
	c := makeTestCipher(t)
	ct, err := c.Seal("sk-test-abcdefg", 42)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Open(ct, 42)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "sk-test-abcdefg" {
		t.Fatalf("plaintext mismatch: %q", plain)
	}
}

func TestOpen_WrongOwnerRejects(t *testing.T) {
	c := makeTestCipher(t)
	ct, _ := c.Seal("sk-test", 42)
	if _, err := c.Open(ct, 99); err == nil {
		t.Fatal("expected AAD mismatch error")
	}
}

func TestLast4(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"a", "a"},
		{"abcd", "abcd"},
		{"sk-abc-XYZW", "XYZW"},
		{"sk-中文测试", "中文测试"},
		{"sk-中文测试1", "文测试1"},
	}
	for _, c := range cases {
		if got := Last4(c.in); got != c.want {
			t.Errorf("Last4(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestNewFromConfig_HKDFFallback(t *testing.T) {
	c1, err := NewFromConfig("", "jwt-secret-x")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := NewFromConfig("", "jwt-secret-x")
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := c1.Seal("hello", 7)
	plain, err := c2.Open(ct, 7)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "hello" {
		t.Fatalf("derived KEK not stable: %q", plain)
	}
}

func TestNewFromConfig_BothEmpty(t *testing.T) {
	if _, err := NewFromConfig("", ""); err == nil {
		t.Fatal("expected error when both KEK sources empty")
	}
}

func TestOpen_CorruptedCiphertext(t *testing.T) {
	c := makeTestCipher(t)
	ct, _ := c.Seal("hello", 1)
	ct[len(ct)-1] ^= 0xff // flip tag byte
	if _, err := c.Open(ct, 1); err == nil {
		t.Fatal("expected error on corrupted ciphertext")
	}
}

func TestOpen_KEKSwitchRejects(t *testing.T) {
	c1 := makeTestCipher(t)
	c2 := makeTestCipher(t)
	ct, _ := c1.Seal("hello", 1)
	if _, err := c2.Open(ct, 1); err == nil {
		t.Fatal("expected error when KEK changed")
	}
}

func TestOpen_TooShort(t *testing.T) {
	c := makeTestCipher(t)
	for _, bad := range [][]byte{nil, {}, make([]byte, 27)} {
		if _, err := c.Open(bad, 1); err == nil {
			t.Fatalf("expected error for %d-byte input", len(bad))
		}
	}
}

func TestNewFromConfig_InvalidBase64(t *testing.T) {
	if _, err := NewFromConfig("not-valid-base64!!!", ""); err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestNewFromConfig_WrongKeyLength(t *testing.T) {
	short16 := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := NewFromConfig(short16, ""); err == nil {
		t.Fatal("expected error for 16-byte KEK")
	}
}

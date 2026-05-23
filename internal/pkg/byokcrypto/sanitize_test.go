package byokcrypto

import (
	"errors"
	"strings"
	"testing"
)

// TestSanitizeDecryptErr_FixedMessage 验证 cipher 内部错误被替换为固定文案，
// 不能把 "AAD" / "mismatch" / "too short" / "open" 等区分信息透传到 client。
// 这是 §5.1 的核心防御：避免 oracle 把"AAD 不符"和"密文长度不对"区分泄露。
func TestSanitizeDecryptErr_FixedMessage(t *testing.T) {
	cases := []string{
		"byokcrypto: open: cipher: message authentication failed",
		"byokcrypto: ciphertext too short",
		"byokcrypto: open: AAD mismatch",
	}
	for _, in := range cases {
		err := SanitizeDecryptErr(errors.New(in))
		if err == nil {
			t.Fatalf("expected non-nil error for input %q", in)
		}
		msg := err.Error()
		for _, banned := range []string{"AAD", "mismatch", "too short", "open", "authentication", "byokcrypto"} {
			if strings.Contains(msg, banned) {
				t.Fatalf("sanitized error must not leak %q, got %q (input %q)", banned, msg, in)
			}
		}
	}
}

// TestSanitizeDecryptErr_NilPassthrough 验证 nil 透传 — 不能把成功路径转成错误。
func TestSanitizeDecryptErr_NilPassthrough(t *testing.T) {
	if SanitizeDecryptErr(nil) != nil {
		t.Fatal("nil should remain nil")
	}
}

// TestSanitizeDecryptErr_StableMessage 验证多次调用返回相同对外文案，
// 防止未来重构里不小心把不同输入映射出不同文案。
func TestSanitizeDecryptErr_StableMessage(t *testing.T) {
	e1 := SanitizeDecryptErr(errors.New("byokcrypto: open: tag mismatch"))
	e2 := SanitizeDecryptErr(errors.New("byokcrypto: ciphertext too short"))
	if e1.Error() != e2.Error() {
		t.Fatalf("different inputs must produce identical sanitized message, got %q vs %q", e1.Error(), e2.Error())
	}
}

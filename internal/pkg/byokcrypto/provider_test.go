package byokcrypto

import "testing"

// TestStaticProvider_ReturnsConfiguredCipher 验证 StaticProvider 把构造时传入的
// *Cipher 原样透传给 GetCipher 调用方。这是 Provider 抽象的最基础契约：
// 注入什么、读出什么。
func TestStaticProvider_ReturnsConfiguredCipher(t *testing.T) {
	c := makeTestCipher(t)
	p := NewStaticProvider(c)
	if got := p.GetCipher(); got != c {
		t.Fatalf("provider returned wrong cipher: got %p, want %p", got, c)
	}
}

// TestStaticProvider_NilSafe 验证 BYOK 未配置场景（cipher 为 nil）下
// Provider 不 panic，GetCipher 返回 nil。fetch handler / handler
// 上游负责对 nil 做防御性返回 error。
func TestStaticProvider_NilSafe(t *testing.T) {
	p := NewStaticProvider(nil)
	if got := p.GetCipher(); got != nil {
		t.Fatalf("nil cipher should produce nil GetCipher, got %p", got)
	}
}

// TestStaticProvider_StableAcrossCalls 验证多次 GetCipher 返回同一指针，
// 保证 caller 可以缓存结果（虽然目前没人缓存，但接口语义不该模糊）。
func TestStaticProvider_StableAcrossCalls(t *testing.T) {
	c := makeTestCipher(t)
	p := NewStaticProvider(c)
	if p.GetCipher() != p.GetCipher() {
		t.Fatal("StaticProvider must return same cipher pointer across calls")
	}
}

package upstream

import (
	"net/http"
	"testing"
)

// has 断言 dst 里 key 的首值等于 want。
func has(t *testing.T, h http.Header, key, want string) {
	t.Helper()
	if got := h.Get(key); got != want {
		t.Fatalf("header %q = %q, want %q", key, got, want)
	}
}

// absent 断言 dst 里没有 key。
func absent(t *testing.T, h http.Header, key string) {
	t.Helper()
	if vals := h.Values(key); len(vals) != 0 {
		t.Fatalf("header %q = %v, want absent", key, vals)
	}
}

func TestForwardClientHeaders_ForwardsSafeAndStripsManaged(t *testing.T) {
	inbound := http.Header{}
	inbound.Set("User-Agent", "claude-cli/1.0")
	inbound.Set("Accept", "text/event-stream")
	inbound.Add("Accept", "application/json")
	inbound.Set("X-Foo", "bar")
	inbound.Set("Authorization", "Bearer sk-client")
	inbound.Set("x-api-key", "client-key")
	inbound.Set("Content-Type", "application/json")
	inbound.Set("Content-Length", "123")
	inbound.Set("Connection", "keep-alive")
	inbound.Set("Accept-Encoding", "gzip, br")
	inbound.Set("Host", "client.example.com")
	inbound.Set("Cookie", "session=abc")
	inbound.Set("X-Forwarded-For", "1.2.3.4")
	inbound.Set("CF-Ray", "xyz")
	inbound.Set("X-Vaala-Request-ID", "rid")
	inbound.Set("Expect", "100-continue")
	inbound.Set("OpenAI-Organization", "org-123")

	dst := http.Header{}
	ForwardClientHeaders(dst, inbound, false)

	has(t, dst, "User-Agent", "claude-cli/1.0")
	has(t, dst, "Accept", "text/event-stream")
	if got := dst.Values("Accept"); len(got) != 2 {
		t.Fatalf("Accept = %v, want 2 forwarded values", got)
	}
	has(t, dst, "X-Foo", "bar")
	for _, k := range []string{
		"Authorization", "x-api-key", "Content-Type", "Content-Length",
		"Connection", "Accept-Encoding", "Host", "Cookie",
		"X-Forwarded-For", "CF-Ray", "X-Vaala-Request-ID",
		"Expect", "OpenAI-Organization",
	} {
		absent(t, dst, k)
	}
}

func TestForwardClientHeaders_BetaSameProtoForwardsCrossProtoStrips(t *testing.T) {
	build := func() http.Header {
		in := http.Header{}
		in.Set("anthropic-beta", "prompt-caching-2024-07-31")
		in.Set("anthropic-version", "2023-06-01")
		in.Set("OpenAI-Beta", "assistants=v2")
		in.Set("X-Stainless-Lang", "js")
		return in
	}

	same := http.Header{}
	ForwardClientHeaders(same, build(), false)
	has(t, same, "anthropic-beta", "prompt-caching-2024-07-31")
	has(t, same, "anthropic-version", "2023-06-01")
	has(t, same, "OpenAI-Beta", "assistants=v2")
	has(t, same, "X-Stainless-Lang", "js")

	cross := http.Header{}
	ForwardClientHeaders(cross, build(), true)
	absent(t, cross, "anthropic-beta")
	absent(t, cross, "anthropic-version")
	absent(t, cross, "OpenAI-Beta")
	absent(t, cross, "X-Stainless-Lang")
}

func TestForwardClientHeaders_StripsEdgeOneKeepsStainlessSameProto(t *testing.T) {
	build := func() http.Header {
		in := http.Header{}
		in.Set("EO-Client-IP", "1.2.3.4")
		in.Set("Eo-Log-Uuid", "abc")
		in.Set("X-Stainless-Timeout", "600")
		in.Set("X-Stainless-Lang", "js")
		return in
	}

	// passthrough / 同协议:EdgeOne 边缘泄漏头剥离,SDK 指纹头(含 Timeout)保留。
	same := http.Header{}
	ForwardClientHeaders(same, build(), false)
	absent(t, same, "EO-Client-IP")
	absent(t, same, "Eo-Log-Uuid")
	has(t, same, "X-Stainless-Timeout", "600")
	has(t, same, "X-Stainless-Lang", "js")

	// 跨协议:EdgeOne 头仍剥离(基础设施泄漏与协议无关);SDK 指纹头此时才按前缀剥。
	cross := http.Header{}
	ForwardClientHeaders(cross, build(), true)
	absent(t, cross, "EO-Client-IP")
	absent(t, cross, "Eo-Log-Uuid")
	absent(t, cross, "X-Stainless-Timeout")
	absent(t, cross, "X-Stainless-Lang")
}

func TestForwardClientHeaders_UAAbsentSuppressedAndCodecHeadersWin(t *testing.T) {
	dst := http.Header{}
	ForwardClientHeaders(dst, http.Header{}, false)
	vals, ok := dst[http.CanonicalHeaderKey("User-Agent")]
	if !ok || len(vals) != 1 || vals[0] != "" {
		t.Fatalf("User-Agent = %v (ok=%v), want explicit empty string", vals, ok)
	}

	dst2 := http.Header{}
	dst2.Set("Authorization", "Bearer channel-key")
	in := http.Header{}
	in.Set("Authorization", "Bearer sk-client")
	ForwardClientHeaders(dst2, in, false)
	has(t, dst2, "Authorization", "Bearer channel-key")
}

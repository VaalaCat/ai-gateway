package oauth

import (
	"context"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

type stubAdapter struct{ tag string }

func (s *stubAdapter) ExchangeCode(_ context.Context, _ *models.OAuthProvider, _ string, _ string) (*TokenResponse, error) {
	return &TokenResponse{AccessToken: s.tag}, nil
}
func (s *stubAdapter) FetchUserinfo(_ context.Context, _ *models.OAuthProvider, _ string) (*UserinfoPayload, error) {
	return &UserinfoPayload{Sub: s.tag}, nil
}

func TestIDPRouter_For(t *testing.T) {
	oidc := &stubAdapter{tag: "oidc"}
	feishu := &stubAdapter{tag: "feishu"}
	r := &IDPRouter{OIDC: oidc, Feishu: feishu}

	cases := []struct {
		protocol string
		want     string
	}{
		{"oidc", "oidc"},
		{"feishu", "feishu"},
		{"unknown", "oidc"},
		{"", "oidc"},
	}
	for _, c := range cases {
		t.Run(c.protocol, func(t *testing.T) {
			tok, _ := r.For(&models.OAuthProvider{Protocol: c.protocol}).ExchangeCode(context.Background(), nil, "", "")
			if tok.AccessToken != c.want {
				t.Fatalf("protocol=%q got %s, want %s", c.protocol, tok.AccessToken, c.want)
			}
		})
	}
}

// 中间状态（Task 2 接 Feishu 前）：protocol=feishu 但 Router.Feishu=nil，
// 必须 fallback 到 OIDC 而不是返回 nil 让 callback 在调用上 panic。
func TestIDPRouter_For_FeishuNilFallback(t *testing.T) {
	oidc := &stubAdapter{tag: "oidc"}
	r := &IDPRouter{OIDC: oidc, Feishu: nil}

	got := r.For(&models.OAuthProvider{Protocol: ProtocolFeishu})
	if got == nil {
		t.Fatal("got nil adapter, want OIDC fallback")
	}
	tok, err := got.ExchangeCode(context.Background(), nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "oidc" {
		t.Fatalf("got %s, want oidc fallback", tok.AccessToken)
	}
}

package models

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateChannelAuth 覆盖合法设置、半填、协议键和传输头校验。
func TestValidateChannelAuth(t *testing.T) {
	// 1. 校验整体配置及不回显值的失败文本。
	for _, test := range []struct {
		name string
		auth ChannelAuth
		want error
	}{
		{"empty", ChannelAuth{}, nil},
		{"bearer", ChannelAuth{Default: &AuthType{"Authorization", "Bearer ${CHANNEL_KEY}"}}, nil},
		{"literal", ChannelAuth{Default: &AuthType{"X-Custom-Token", "private-secret"}}, nil},
		{"protocol", ChannelAuth{Protocols: map[string]AuthType{"messages": {"X-Api-Key", "${CHANNEL_KEY}"}, "chat_completions": {"Authorization", "literal"}, "responses": {"Authorization", "literal"}}}, nil},
		{"invalid name", ChannelAuth{Default: &AuthType{"X Auth", "private-secret"}}, ErrUpstreamAuthHeader},
		{"missing header", ChannelAuth{Default: &AuthType{Value: "private-secret"}}, ErrUpstreamAuthHeader},
		{"missing value", ChannelAuth{Default: &AuthType{Header: "X-Token"}}, ErrUpstreamAuthValue},
		{"empty protocol item", ChannelAuth{Protocols: map[string]AuthType{"messages": {}}}, ErrUpstreamAuthHeader},
		{"unsupported protocol", ChannelAuth{Protocols: map[string]AuthType{"claude-private-secret": {"X-Token", "private-secret"}}}, ErrUpstreamAuthProtocol},
		{"crlf", ChannelAuth{Default: &AuthType{"X-Token", "private-secret\r\nX: bad"}}, ErrUpstreamAuthValue},
		{"control", ChannelAuth{Default: &AuthType{"X-Token", "private-secret\x00"}}, ErrUpstreamAuthValue},
		{"unknown variable", ChannelAuth{Default: &AuthType{"X-Token", "${PRIVATE_SECRET}"}}, ErrUpstreamAuthValue},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateChannelAuth(test.auth)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if err != nil && strings.Contains(err.Error(), "private-secret") {
				t.Fatal("secret leaked")
			}
		})
	}
	// 2. 传输和内部头按大小写无关规则拒绝。
	for _, header := range []string{"Host", "content-length", "Content-Type", "Content-Encoding", "Accept-Encoding", "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade", "x-VaaLa-Route"} {
		t.Run(header, func(t *testing.T) {
			if !errors.Is(ValidateChannelAuth(ChannelAuth{Default: &AuthType{header, "private-secret"}}), ErrUpstreamAuthHeader) {
				t.Fatal("managed header accepted")
			}
		})
	}
}

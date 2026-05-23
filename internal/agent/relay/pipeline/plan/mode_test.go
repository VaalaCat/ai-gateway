package plan

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"

	// Register codec implementations so GetInbound / GetOutbound 不是 nil
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/claude"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
)

// TestModePicker_Legacy: success — channel 显式 UseLegacyAdaptor → legacy。
func TestModePicker_Legacy(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeOpenAI, UseLegacyAdaptor: true}}
	got := defaultModePicker{}.Pick(ch, "gpt-4", codec.ProtocolOpenAIChat)
	if got != state.ModeLegacy {
		t.Errorf("UseLegacyAdaptor=true → legacy, got %q", got)
	}
}

// TestModePicker_LegacyUnknownProtocol: success — inbound Unknown → 没 native codec，回退 legacy。
func TestModePicker_LegacyUnknownProtocol(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeOpenAI}}
	got := defaultModePicker{}.Pick(ch, "gpt-4", codec.ProtocolUnknown)
	if got != state.ModeLegacy {
		t.Errorf("ProtocolUnknown → legacy, got %q", got)
	}
}

// TestModePicker_Passthrough: success — channel PassthroughEnabled + inbound == outbound → passthrough。
func TestModePicker_Passthrough(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{
		Type:               consts.ChannelTypeOpenAI, // ChannelTypeToProtocol → OpenAIChat
		PassthroughEnabled: true,
	}}
	got := defaultModePicker{}.Pick(ch, "gpt-4", codec.ProtocolOpenAIChat)
	if got != state.ModePassthrough {
		t.Errorf("Passthrough channel + inbound==outbound → passthrough, got %q", got)
	}
}

// TestModePicker_NativeDefault: success — 普通 OpenAI channel → native。
func TestModePicker_NativeDefault(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeOpenAI}}
	got := defaultModePicker{}.Pick(ch, "gpt-4", codec.ProtocolOpenAIChat)
	if got != state.ModeNative {
		t.Errorf("default OpenAI channel → native, got %q", got)
	}
}

// TestModePicker_LegacyOverPassthrough: boundary — 两个标志同时 set，legacy 优先。
// 复刻原 (*Handler) 顺序：shouldUseLegacy 先判，true 就直接返回。
func TestModePicker_LegacyOverPassthrough(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeOpenAI, UseLegacyAdaptor: true, PassthroughEnabled: true}}
	got := defaultModePicker{}.Pick(ch, "gpt-4", codec.ProtocolOpenAIChat)
	if got != state.ModeLegacy {
		t.Errorf("legacy should win over passthrough, got %q", got)
	}
}

// TestModePicker_NilChannel: boundary — nil channel 不 panic，默认走 native。
// （生产路径不会传 nil；这只是契约防御测试）。
func TestModePicker_NilChannel(t *testing.T) {
	got := defaultModePicker{}.Pick(nil, "gpt-4", codec.ProtocolOpenAIChat)
	if got != state.ModeNative {
		t.Errorf("nil channel → native (defensive), got %q", got)
	}
}

// TestModePicker_PassthroughOnlyWhenProtoMatches: boundary —
// PassthroughEnabled=true 但 inbound != outbound 协议（Claude inbound + OpenAI channel），
// shouldPassthrough 返 false → 走 native。
func TestModePicker_PassthroughOnlyWhenProtoMatches(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{
		Type:               consts.ChannelTypeOpenAI, // outbound 会被协商成 OpenAIChat
		PassthroughEnabled: true,
	}}
	// Claude inbound + OpenAI channel → outbound = OpenAIChat ≠ Claude → not passthrough
	got := defaultModePicker{}.Pick(ch, "gpt-4", codec.ProtocolClaude)
	if got != state.ModeNative {
		t.Errorf("inbound!=outbound 时 Passthrough 应失效 → native, got %q", got)
	}
}

// TestShouldPassthrough: 直测 shouldPassthrough 各分支组合。
// 从 internal/agent/relay/passthrough_test.go 迁入——shouldPassthrough
// 拆到 plan 子包后属于包级私有函数，单测只能放在同包内。
func TestShouldPassthrough(t *testing.T) {
	tests := []struct {
		name        string
		passthrough bool
		supported   string
		channelType int
		inbound     codec.Protocol
		want        bool
	}{
		{"disabled", false, `["responses"]`, 1, codec.ProtocolOpenAIResponses, false},
		{"enabled same protocol", true, `["responses"]`, 1, codec.ProtocolOpenAIResponses, true},
		{"enabled different protocol", true, `["chat-completion"]`, 1, codec.ProtocolOpenAIResponses, false},
		{"enabled no supported types defaults to chat", true, "", 1, codec.ProtocolOpenAIChat, true},
		{"enabled both supported inbound matches", true, `["responses","chat-completion"]`, 1, codec.ProtocolOpenAIResponses, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &models.Channel{ChannelCore: models.ChannelCore{PassthroughEnabled: tt.passthrough, SupportedAPITypes: tt.supported, Type: tt.channelType}}
			got := shouldPassthrough(ch, tt.inbound, "")
			if got != tt.want {
				t.Errorf("shouldPassthrough = %v, want %v", got, tt.want)
			}
		})
	}
}

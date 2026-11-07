package plan

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

// TestModePicker_Legacy: success — channel 显式 UseLegacyAdaptor → legacy。
func TestModePicker_Legacy(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeOpenAI, UseLegacyAdaptor: true}}
	got := defaultModePicker{}.Pick(ch, "gpt-4", llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{})
	if got != state.ModeLegacy {
		t.Errorf("UseLegacyAdaptor=true → legacy, got %q", got)
	}
}

// TestModePicker_LegacyUnknownProtocol: success — inbound Unknown → 没 native codec，回退 legacy。
func TestModePicker_LegacyUnknownProtocol(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeOpenAI}}
	got := defaultModePicker{}.Pick(ch, "gpt-4", llmkit.ProtocolUnknown, llmkit.ToolCallRequestInfo{})
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
	got := defaultModePicker{}.Pick(ch, "gpt-4", llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{})
	if got != state.ModePassthrough {
		t.Errorf("Passthrough channel + inbound==outbound → passthrough, got %q", got)
	}
}

// behavior change: a tool fallback that rewrites request tools must run through
// the native codec even when same-protocol passthrough is otherwise enabled.
func TestModePicker_FunctionToolFallbackDisablesPassthrough(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{
		Type:               consts.ChannelTypeOpenAI,
		SupportedAPITypes:  `["responses"]`,
		PassthroughEnabled: true,
		OtherSettings:      `{"builtin_tool_fallback":"function"}`,
	}}
	got := defaultModePicker{}.Pick(ch, "glm-5.2", llmkit.ProtocolOpenAIResponses, llmkit.ToolCallRequestInfo{})
	if got != state.ModeNative {
		t.Fatalf("function tool fallback requires native mode, got %q", got)
	}
}

func TestModePicker_FunctionToolFallbackKeepsChatPassthrough(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{
		Type:               consts.ChannelTypeOpenAI,
		SupportedAPITypes:  `["chat_completions"]`,
		PassthroughEnabled: true,
		OtherSettings:      `{"builtin_tool_fallback":"function"}`,
	}}
	got := defaultModePicker{}.Pick(ch, "glm-5.2", llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{})
	if got != state.ModePassthrough {
		t.Fatalf("chat channel does not use Responses function fallback, got %q", got)
	}
}

// TestModePicker_NativeDefault: success — 普通 OpenAI channel → native。
func TestModePicker_NativeDefault(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeOpenAI}}
	got := defaultModePicker{}.Pick(ch, "gpt-4", llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{})
	if got != state.ModeNative {
		t.Errorf("default OpenAI channel → native, got %q", got)
	}
}

// TestModePicker_LegacyOverPassthrough: boundary — 两个标志同时 set，legacy 优先。
// 复刻原 (*Handler) 顺序：shouldUseLegacy 先判，true 就直接返回。
func TestModePicker_LegacyOverPassthrough(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeOpenAI, UseLegacyAdaptor: true, PassthroughEnabled: true}}
	got := defaultModePicker{}.Pick(ch, "gpt-4", llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{})
	if got != state.ModeLegacy {
		t.Errorf("legacy should win over passthrough, got %q", got)
	}
}

// TestModePicker_NilChannel: boundary — nil channel 不 panic，默认走 native。
// （生产路径不会传 nil；这只是契约防御测试）。
func TestModePicker_NilChannel(t *testing.T) {
	got := defaultModePicker{}.Pick(nil, "gpt-4", llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{})
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
	got := defaultModePicker{}.Pick(ch, "gpt-4", llmkit.ProtocolClaude, llmkit.ToolCallRequestInfo{})
	if got != state.ModeNative {
		t.Errorf("inbound!=outbound 时 Passthrough 应失效 → native, got %q", got)
	}
}

// TestModePicker_ToolCallsOnlyForceNativeForChatOutboundOrPackedID: 工具调用特征只在
// 出口是 Chat 或历史里有打包 ID 时才让透传失效；Claude→Claude 带工具仍透传。
func TestModePicker_ToolCallsOnlyForceNativeForChatOutboundOrPackedID(t *testing.T) {
	tests := []struct {
		name    string
		channel int
		inbound llmkit.Protocol
		info    llmkit.ToolCallRequestInfo
		want    state.RelayMode
	}{
		{"claude to claude with tools", consts.ChannelTypeAnthropic, llmkit.ProtocolClaude, llmkit.ToolCallRequestInfo{HasTools: true, HasToolHistory: true}, state.ModePassthrough},
		{"claude to claude with packed id", consts.ChannelTypeAnthropic, llmkit.ProtocolClaude, llmkit.ToolCallRequestInfo{HasToolHistory: true, HasPackedToolCallID: true}, state.ModeNative},
		{"chat outbound with tools", consts.ChannelTypeOpenAI, llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{HasTools: true}, state.ModeNative},
		{"chat outbound text", consts.ChannelTypeOpenAI, llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{}, state.ModePassthrough},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &models.Channel{ChannelCore: models.ChannelCore{Type: tt.channel, PassthroughEnabled: true}}
			if got := (defaultModePicker{}).Pick(ch, "gpt-4", tt.inbound, tt.info); got != tt.want {
				t.Errorf("want %q, got %q", tt.want, got)
			}
		})
	}
}

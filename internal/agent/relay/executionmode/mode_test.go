package executionmode

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
)

func TestForChannelPreservesExecutionModeParity(t *testing.T) {
	tests := []struct {
		name    string
		channel *models.Channel
		model   string
		inbound llmkit.Protocol
		want    attemptproxy.ExecutionMode
	}{
		{
			name: "explicit legacy",
			channel: &models.Channel{ChannelCore: models.ChannelCore{
				Type:             consts.ChannelTypeOpenAI,
				UseLegacyAdaptor: true,
			}},
			model: "gpt-4", inbound: llmkit.ProtocolOpenAIChat, want: attemptproxy.ModeLegacy,
		},
		{
			name: "unknown inbound uses legacy",
			channel: &models.Channel{ChannelCore: models.ChannelCore{
				Type: consts.ChannelTypeOpenAI,
			}},
			model: "gpt-4", inbound: llmkit.ProtocolUnknown, want: attemptproxy.ModeLegacy,
		},
		{
			name: "same protocol passthrough",
			channel: &models.Channel{ChannelCore: models.ChannelCore{
				Type:               consts.ChannelTypeOpenAI,
				PassthroughEnabled: true,
			}},
			model: "gpt-4", inbound: llmkit.ProtocolOpenAIChat, want: attemptproxy.ModePassthrough,
		},
		{
			name: "responses builtin tool fallback uses native",
			channel: &models.Channel{ChannelCore: models.ChannelCore{
				Type:               consts.ChannelTypeOpenAI,
				SupportedAPITypes:  `["responses"]`,
				PassthroughEnabled: true,
				OtherSettings:      `{"builtin_tool_fallback":"function"}`,
			}},
			model: "glm-5.2", inbound: llmkit.ProtocolOpenAIResponses, want: attemptproxy.ModeNative,
		},
		{
			name: "chat ignores responses builtin tool fallback",
			channel: &models.Channel{ChannelCore: models.ChannelCore{
				Type:               consts.ChannelTypeOpenAI,
				SupportedAPITypes:  `["chat_completions"]`,
				PassthroughEnabled: true,
				OtherSettings:      `{"builtin_tool_fallback":"function"}`,
			}},
			model: "glm-5.2", inbound: llmkit.ProtocolOpenAIChat, want: attemptproxy.ModePassthrough,
		},
		{
			name: "native default",
			channel: &models.Channel{ChannelCore: models.ChannelCore{
				Type: consts.ChannelTypeOpenAI,
			}},
			model: "gpt-4", inbound: llmkit.ProtocolOpenAIChat, want: attemptproxy.ModeNative,
		},
		{
			name: "legacy wins over passthrough",
			channel: &models.Channel{ChannelCore: models.ChannelCore{
				Type:               consts.ChannelTypeOpenAI,
				UseLegacyAdaptor:   true,
				PassthroughEnabled: true,
			}},
			model: "gpt-4", inbound: llmkit.ProtocolOpenAIChat, want: attemptproxy.ModeLegacy,
		},
		{
			name:  "nil channel uses native",
			model: "gpt-4", inbound: llmkit.ProtocolOpenAIChat, want: attemptproxy.ModeNative,
		},
		{
			name: "different protocol uses native",
			channel: &models.Channel{ChannelCore: models.ChannelCore{
				Type:               consts.ChannelTypeOpenAI,
				PassthroughEnabled: true,
			}},
			model: "gpt-4", inbound: llmkit.ProtocolClaude, want: attemptproxy.ModeNative,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ForChannel(tt.channel, tt.model, tt.inbound, llmkit.ToolCallRequestInfo{}))
		})
	}
}

func TestMappedModelRulesSelectNative(t *testing.T) {
	// 1. 构造支持直通的 Claude 渠道，按规则选择是否执行转换。
	cases := []struct {
		name     string
		settings string
		want     attemptproxy.ExecutionMode
	}{
		{"thinking original", `{"model_thinking_passthrough":[{"model_pattern":"^deepseek-","send_back_thinking":true}]}`, attemptproxy.ModeNative},
		{"thinking mapped", `{"model_thinking_passthrough":[{"model_pattern":"^ep-123$","send_back_thinking":true}]}`, attemptproxy.ModeNative},
		{"thinking first disabled", `{"model_thinking_passthrough":[{"model_pattern":"^ep-123$","send_back_thinking":false},{"model_pattern":"^deepseek-","send_back_thinking":true}]}`, attemptproxy.ModePassthrough},
		{"schema original", `{"model_tool_schema_compatibility":[{"model_pattern":"^deepseek-","profile":"gemini_compat"}]}`, attemptproxy.ModeNative},
		{"schema first none", `{"model_tool_schema_compatibility":[{"model_pattern":"^ep-123$","profile":"none"},{"model_pattern":"^deepseek-","profile":"gemini_compat"}]}`, attemptproxy.ModePassthrough},
		{"no rules", "", attemptproxy.ModePassthrough},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := &models.Channel{
				ChannelCore:  models.ChannelCore{Type: consts.ChannelTypeAnthropic, PassthroughEnabled: true, OtherSettings: tc.settings},
				ModelMapping: `{"deepseek-v4.1-flash":"ep-123"}`,
			}
			// 2. 工具历史需要回填或改写时进入 Native，首条关闭规则保留直通。
			require.Equal(t, tc.want, ForChannel(ch, "deepseek-v4.1-flash", llmkit.ProtocolClaudeMessages, llmkit.ToolCallRequestInfo{HasToolHistory: true}))
		})
	}
}

func TestForChannelPreservesPassthroughCombinations(t *testing.T) {
	tests := []struct {
		name        string
		passthrough bool
		supported   string
		inbound     llmkit.Protocol
		want        attemptproxy.ExecutionMode
	}{
		{"disabled", false, `["responses"]`, llmkit.ProtocolOpenAIResponses, attemptproxy.ModeNative},
		{"enabled same protocol", true, `["responses"]`, llmkit.ProtocolOpenAIResponses, attemptproxy.ModePassthrough},
		{"enabled different protocol", true, `["chat-completion"]`, llmkit.ProtocolOpenAIResponses, attemptproxy.ModeNative},
		{"enabled no supported types defaults to chat", true, "", llmkit.ProtocolOpenAIChat, attemptproxy.ModePassthrough},
		{"enabled both supported inbound matches", true, `["responses","chat-completion"]`, llmkit.ProtocolOpenAIResponses, attemptproxy.ModePassthrough},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &models.Channel{ChannelCore: models.ChannelCore{
				Type:               consts.ChannelTypeOpenAI,
				PassthroughEnabled: tt.passthrough,
				SupportedAPITypes:  tt.supported,
			}}
			require.Equal(t, tt.want, ForChannel(ch, "", tt.inbound, llmkit.ToolCallRequestInfo{}))
		})
	}
}

func TestToolSchemaCompatibilityBypassesPassthroughForMappedModel(t *testing.T) {
	ch := &models.Channel{
		ChannelCore: models.ChannelCore{
			Type: consts.ChannelTypeOpenAI, PassthroughEnabled: true,
			OtherSettings: `{"model_tool_schema_compatibility":[
				{"model_pattern":"^gemini-excluded$","profile":"none","signature_fallback":"none"},
				{"model_pattern":"^gemini-fallback$","profile":"none","signature_fallback":"gemini"},
				{"model_pattern":"^gemini-.*$","profile":"gemini_compat"}
			]}`,
		},
		ModelMapping: `{"alias":"gemini-4-argon","excluded":"gemini-excluded","reverse-alias":"gpt-5"}`,
	}
	for _, tt := range []struct {
		model string
		want  attemptproxy.ExecutionMode
	}{
		{"alias", attemptproxy.ModeNative},
		{"gemini-4-argon", attemptproxy.ModeNative},
		{"gemini-fallback", attemptproxy.ModePassthrough},
		{"excluded", attemptproxy.ModePassthrough},
		{"reverse-alias", attemptproxy.ModePassthrough},
		{"gpt-5", attemptproxy.ModePassthrough},
	} {
		t.Run(tt.model, func(t *testing.T) {
			require.Equal(t, tt.want, ForChannel(ch, tt.model, llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{}))
		})
	}
}

func TestForChannelToolCalls(t *testing.T) {
	openAIChat := func() *models.Channel {
		return &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeOpenAI, PassthroughEnabled: true}}
	}
	openAIResponses := func() *models.Channel {
		return &models.Channel{ChannelCore: models.ChannelCore{
			Type: consts.ChannelTypeOpenAI, PassthroughEnabled: true, SupportedAPITypes: `["responses"]`,
		}}
	}
	claude := func() *models.Channel {
		return &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeAnthropic, PassthroughEnabled: true}}
	}
	for _, tt := range []struct {
		name    string
		channel *models.Channel
		inbound llmkit.Protocol
		info    llmkit.ToolCallRequestInfo
		want    attemptproxy.ExecutionMode
	}{
		{"claude to claude with tools", claude(), llmkit.ProtocolClaude, llmkit.ToolCallRequestInfo{HasTools: true}, attemptproxy.ModePassthrough},
		{"claude to claude with tool history", claude(), llmkit.ProtocolClaude, llmkit.ToolCallRequestInfo{HasTools: true, HasToolHistory: true}, attemptproxy.ModePassthrough},
		{"claude to claude with packed id", claude(), llmkit.ProtocolClaude, llmkit.ToolCallRequestInfo{HasToolHistory: true, HasPackedToolCallID: true}, attemptproxy.ModeNative},
		{"claude to claude text", claude(), llmkit.ProtocolClaude, llmkit.ToolCallRequestInfo{}, attemptproxy.ModePassthrough},
		{"responses to responses with tools", openAIResponses(), llmkit.ProtocolOpenAIResponses, llmkit.ToolCallRequestInfo{HasTools: true}, attemptproxy.ModePassthrough},
		{"responses to responses with packed id", openAIResponses(), llmkit.ProtocolOpenAIResponses, llmkit.ToolCallRequestInfo{HasToolHistory: true, HasPackedToolCallID: true}, attemptproxy.ModeNative},
		{"chat outbound with tools", openAIChat(), llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{HasTools: true}, attemptproxy.ModeNative},
		{"chat outbound with tool history", openAIChat(), llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{HasToolHistory: true}, attemptproxy.ModeNative},
		{"chat outbound with packed id", openAIChat(), llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{HasPackedToolCallID: true}, attemptproxy.ModeNative},
		{"chat outbound text", openAIChat(), llmkit.ProtocolOpenAIChat, llmkit.ToolCallRequestInfo{}, attemptproxy.ModePassthrough},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ForChannel(tt.channel, "gemini", tt.inbound, tt.info))
			tt.channel.UseLegacyAdaptor = true
			require.Equal(t, attemptproxy.ModeLegacy, ForChannel(tt.channel, "gemini", tt.inbound, tt.info))
		})
	}
}

func TestForChannelSignatureFallbackFollowsToolCallRule(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{
		Type: consts.ChannelTypeOpenAI, PassthroughEnabled: true,
		OtherSettings: `{"model_tool_schema_compatibility":[{"model_pattern":"^gemini-.*$","profile":"none","signature_fallback":"gemini"}]}`,
	}}
	for _, tt := range []struct {
		name string
		info llmkit.ToolCallRequestInfo
		want attemptproxy.ExecutionMode
	}{
		{"text", llmkit.ToolCallRequestInfo{}, attemptproxy.ModePassthrough},
		{"tools", llmkit.ToolCallRequestInfo{HasTools: true}, attemptproxy.ModeNative},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ForChannel(ch, "gemini-3", llmkit.ProtocolOpenAIChat, tt.info))
		})
	}
}

func TestGeminiNativeInboundAlwaysPassthrough(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{Type: consts.ChannelTypeGemini, PassthroughEnabled: false, OtherSettings: `{"model_tool_schema_compatibility":[{"model_pattern":".*","profile":"gemini_compat"}]}`}}
	got := ForChannel(ch, "gemini", llmkit.ProtocolGemini, llmkit.ToolCallRequestInfo{HasPackedToolCallID: true, HasToolHistory: true, HasTools: true})
	require.Equal(t, attemptproxy.ModePassthrough, got)
}

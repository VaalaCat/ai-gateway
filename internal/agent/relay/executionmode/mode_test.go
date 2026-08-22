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
			require.Equal(t, tt.want, ForChannel(tt.channel, tt.model, tt.inbound))
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
			require.Equal(t, tt.want, ForChannel(ch, "", tt.inbound))
		})
	}
}

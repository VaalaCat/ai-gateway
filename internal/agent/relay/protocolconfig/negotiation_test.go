package protocolconfig

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestJoinUpstreamURL(t *testing.T) {
	t.Run("normal path preserved", func(t *testing.T) {
		got, err := JoinUpstreamURL("https://api.openai.com", "/v1/chat/completions")
		if err != nil || got != "https://api.openai.com/v1/chat/completions" {
			t.Fatalf("got %q err %v", got, err)
		}
	})
	t.Run("trailing slash on base", func(t *testing.T) {
		got, err := JoinUpstreamURL("https://api.openai.com/", "/v1/messages")
		if err != nil || got != "https://api.openai.com/v1/messages" {
			t.Fatalf("got %q err %v", got, err)
		}
	})
	t.Run("at-sign exfil rejected", func(t *testing.T) {
		if _, err := JoinUpstreamURL("https://api.openai.com", "@evil.example/v1/chat/completions"); err == nil {
			t.Fatal("want error, got nil")
		}
	})
	t.Run("suffix host change rejected", func(t *testing.T) {
		if _, err := JoinUpstreamURL("https://api.openai.com", ".evil.com/v1/chat/completions"); err == nil {
			t.Fatal("want error, got nil")
		}
	})
	t.Run("invalid base rejected", func(t *testing.T) {
		if _, err := JoinUpstreamURL("not-a-url", "/v1/x"); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

func TestNegotiateOutboundProtocol_Override(t *testing.T) {
	endpointsAll := `{"chat_completions":"/v1/chat/completions","responses":"/v1/responses","messages":"/v1/messages"}`
	endpointsChatOnly := `{"chat_completions":"/v1/chat/completions"}`

	tests := []struct {
		name      string
		inbound   llmkit.Protocol
		chType    int
		supported string
		endpoints string
		override  map[llmkit.Protocol]llmkit.Protocol
		want      llmkit.Protocol
	}{
		{
			name:      "override hit and target in endpoints",
			inbound:   llmkit.ProtocolOpenAIChat,
			endpoints: endpointsAll,
			override:  map[llmkit.Protocol]llmkit.Protocol{llmkit.ProtocolOpenAIChat: llmkit.ProtocolClaudeMessages},
			want:      llmkit.ProtocolClaudeMessages,
		},
		{
			name:      "override hit but target not in endpoints, fallback to default",
			inbound:   llmkit.ProtocolOpenAIChat,
			endpoints: endpointsChatOnly,
			override:  map[llmkit.Protocol]llmkit.Protocol{llmkit.ProtocolOpenAIChat: llmkit.ProtocolClaudeMessages},
			want:      llmkit.ProtocolOpenAIChat,
		},
		{
			name:      "override hit and target equals inbound (explicit identity)",
			inbound:   llmkit.ProtocolOpenAIChat,
			endpoints: endpointsAll,
			override:  map[llmkit.Protocol]llmkit.Protocol{llmkit.ProtocolOpenAIChat: llmkit.ProtocolOpenAIChat},
			want:      llmkit.ProtocolOpenAIChat,
		},
		{
			name:      "override miss for this inbound, walk default",
			inbound:   llmkit.ProtocolOpenAIResponses,
			endpoints: endpointsAll,
			override:  map[llmkit.Protocol]llmkit.Protocol{llmkit.ProtocolOpenAIChat: llmkit.ProtocolClaudeMessages},
			want:      llmkit.ProtocolOpenAIResponses,
		},
		{
			name:      "nil override equals no override",
			inbound:   llmkit.ProtocolOpenAIChat,
			endpoints: endpointsAll,
			override:  nil,
			want:      llmkit.ProtocolOpenAIChat,
		},
		{
			name:      "empty endpoints + override + channelType fallback",
			inbound:   llmkit.ProtocolOpenAIChat,
			chType:    14, // ChannelTypeAnthropic
			endpoints: "",
			supported: "",
			override:  map[llmkit.Protocol]llmkit.Protocol{llmkit.ProtocolOpenAIChat: llmkit.ProtocolClaudeMessages},
			want:      llmkit.ProtocolClaudeMessages,
		},
		{
			name:      "override target llmkit.ProtocolUnknown ignored",
			inbound:   llmkit.ProtocolOpenAIChat,
			endpoints: endpointsAll,
			override:  map[llmkit.Protocol]llmkit.Protocol{llmkit.ProtocolOpenAIChat: llmkit.ProtocolUnknown},
			want:      llmkit.ProtocolOpenAIChat,
		},
		{
			name:      "override target llmkit.ProtocolGemini ignored",
			inbound:   llmkit.ProtocolOpenAIChat,
			endpoints: endpointsAll,
			override:  map[llmkit.Protocol]llmkit.Protocol{llmkit.ProtocolOpenAIChat: llmkit.ProtocolGemini},
			want:      llmkit.ProtocolOpenAIChat,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NegotiateOutboundProtocol(tc.inbound, tc.chType, tc.supported, tc.endpoints, tc.override)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

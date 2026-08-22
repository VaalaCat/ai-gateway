package protocolconfig

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestPathToProtocol(t *testing.T) {
	tests := []struct {
		path string
		want llmkit.Protocol
	}{
		{"/v1/chat/completions", llmkit.ProtocolOpenAIChat},
		{"/v1/responses", llmkit.ProtocolOpenAIResponses},
		{"/v1/messages", llmkit.ProtocolClaudeMessages},
		{"/v1/embeddings", llmkit.ProtocolUnknown},
		{"/health", llmkit.ProtocolUnknown},
		{"", llmkit.ProtocolUnknown},
		{"/v1/chat/completions/", llmkit.ProtocolUnknown},
		{"/v1/messages/batch", llmkit.ProtocolUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := PathToProtocol(tc.path)
			if got != tc.want {
				t.Errorf("PathToProtocol(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestChannelTypeToProtocol(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		want        llmkit.Protocol
	}{
		{"claude channel", 14, llmkit.ProtocolClaudeMessages},
		{"openai channel type 1", 1, llmkit.ProtocolOpenAIChat},
		{"openai channel type 0", 0, llmkit.ProtocolOpenAIChat},
		{"openai channel type 3", 3, llmkit.ProtocolOpenAIChat},
		{"negative channel type", -1, llmkit.ProtocolOpenAIChat},
		{"large channel type", 999, llmkit.ProtocolOpenAIChat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ChannelTypeToProtocol(tc.channelType)
			if got != tc.want {
				t.Errorf("ChannelTypeToProtocol(%d) = %q, want %q", tc.channelType, got, tc.want)
			}
		})
	}
}

func TestNegotiateOutboundProtocol(t *testing.T) {
	tests := []struct {
		name              string
		inbound           llmkit.Protocol
		channelType       int
		supportedAPITypes string
		want              llmkit.Protocol
	}{
		// empty config → fallback to default
		{"empty supported, openai channel, chat inbound", llmkit.ProtocolOpenAIChat, 1, "", llmkit.ProtocolOpenAIChat},
		{"empty supported, claude channel, chat inbound", llmkit.ProtocolOpenAIChat, 14, "", llmkit.ProtocolClaudeMessages},
		// passthrough
		{"passthrough chat", llmkit.ProtocolOpenAIChat, 1, `["chat-completion","responses"]`, llmkit.ProtocolOpenAIChat},
		{"passthrough responses", llmkit.ProtocolOpenAIResponses, 1, `["chat-completion","responses"]`, llmkit.ProtocolOpenAIResponses},
		{"passthrough claude", llmkit.ProtocolClaudeMessages, 14, `["claude","chat-completion"]`, llmkit.ProtocolClaudeMessages},
		// fallback by priority
		{"fallback responses>chat", llmkit.ProtocolClaudeMessages, 1, `["chat-completion","responses"]`, llmkit.ProtocolOpenAIResponses},
		{"fallback chat only", llmkit.ProtocolClaudeMessages, 1, `["chat-completion"]`, llmkit.ProtocolOpenAIChat},
		{"fallback claude only", llmkit.ProtocolOpenAIChat, 14, `["claude"]`, llmkit.ProtocolClaudeMessages},
		{"fallback responses only", llmkit.ProtocolOpenAIChat, 1, `["responses"]`, llmkit.ProtocolOpenAIResponses},
		// invalid JSON → fallback
		{"invalid json", llmkit.ProtocolOpenAIChat, 1, `not-json`, llmkit.ProtocolOpenAIChat},
		// unknown types ignored
		{"unknown types ignored", llmkit.ProtocolOpenAIChat, 1, `["unknown"]`, llmkit.ProtocolOpenAIChat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NegotiateOutboundProtocol(tc.inbound, tc.channelType, tc.supportedAPITypes, "", nil)
			if got != tc.want {
				t.Errorf("NegotiateOutboundProtocol(%q, %d, %q) = %q, want %q",
					tc.inbound, tc.channelType, tc.supportedAPITypes, got, tc.want)
			}
		})
	}
}

func TestDefaultEndpointPaths(t *testing.T) {
	tests := []struct {
		proto llmkit.Protocol
		want  string
	}{
		{llmkit.ProtocolOpenAIChat, "/v1/chat/completions"},
		{llmkit.ProtocolOpenAIResponses, "/v1/responses"},
		{llmkit.ProtocolClaudeMessages, "/v1/messages"},
		{llmkit.ProtocolUnknown, ""},
	}
	for _, tc := range tests {
		t.Run(string(tc.proto), func(t *testing.T) {
			got := DefaultEndpointPath(tc.proto)
			if got != tc.want {
				t.Errorf("DefaultEndpointPath(%q) = %q, want %q", tc.proto, got, tc.want)
			}
		})
	}
}

func TestParseEndpoints(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[llmkit.Protocol]string
	}{
		{"empty", "", nil},
		{"invalid json", "not-json", nil},
		{"single chat", `{"chat_completions":"/v1/chat/completions"}`, map[llmkit.Protocol]string{llmkit.ProtocolOpenAIChat: "/v1/chat/completions"}},
		{"all three", `{"chat_completions":"/api/chat","responses":"/api/resp","messages":"/api/msg"}`,
			map[llmkit.Protocol]string{llmkit.ProtocolOpenAIChat: "/api/chat", llmkit.ProtocolOpenAIResponses: "/api/resp", llmkit.ProtocolClaudeMessages: "/api/msg"}},
		{"unknown keys ignored", `{"chat_completions":"/v1/cc","unknown":"/foo"}`, map[llmkit.Protocol]string{llmkit.ProtocolOpenAIChat: "/v1/cc"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseEndpoints(tc.raw)
			if len(got) != len(tc.want) {
				t.Errorf("ParseEndpoints(%q) len = %d, want %d", tc.raw, len(got), len(tc.want))
				return
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("ParseEndpoints(%q)[%q] = %q, want %q", tc.raw, k, got[k], v)
				}
			}
		})
	}
}

func TestResolveEndpointPath(t *testing.T) {
	tests := []struct {
		name      string
		endpoints string
		proto     llmkit.Protocol
		want      string
	}{
		{"from endpoints", `{"chat_completions":"/custom/cc"}`, llmkit.ProtocolOpenAIChat, "/custom/cc"},
		{"from endpoints claude", `{"messages":"/custom/msg"}`, llmkit.ProtocolClaudeMessages, "/custom/msg"},
		{"fallback default chat", "", llmkit.ProtocolOpenAIChat, "/v1/chat/completions"},
		{"fallback default responses", "", llmkit.ProtocolOpenAIResponses, "/v1/responses"},
		{"fallback default claude", "", llmkit.ProtocolClaudeMessages, "/v1/messages"},
		{"proto not in endpoints uses default", `{"chat_completions":"/cc"}`, llmkit.ProtocolClaudeMessages, "/v1/messages"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveEndpointPath(tc.endpoints, tc.proto)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNegotiateOutboundProtocol_Endpoints(t *testing.T) {
	tests := []struct {
		name      string
		inbound   llmkit.Protocol
		chType    int
		supported string
		endpoints string
		want      llmkit.Protocol
	}{
		{"endpoints passthrough chat", llmkit.ProtocolOpenAIChat, 1, "", `{"chat_completions":"/v1/cc"}`, llmkit.ProtocolOpenAIChat},
		{"endpoints fallback responses>chat", llmkit.ProtocolClaudeMessages, 1, "", `{"chat_completions":"/cc","responses":"/r"}`, llmkit.ProtocolOpenAIResponses},
		{"endpoints only claude", llmkit.ProtocolOpenAIChat, 1, "", `{"messages":"/m"}`, llmkit.ProtocolClaudeMessages},
		{"empty endpoints uses supported", llmkit.ProtocolOpenAIChat, 1, `["responses"]`, "", llmkit.ProtocolOpenAIResponses},
		{"empty both uses type", llmkit.ProtocolOpenAIChat, 14, "", "", llmkit.ProtocolClaudeMessages},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NegotiateOutboundProtocol(tc.inbound, tc.chType, tc.supported, tc.endpoints, nil)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

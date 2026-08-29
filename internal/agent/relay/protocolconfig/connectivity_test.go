package protocolconfig

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestNormalizeEndpointType(t *testing.T) {
	cases := []struct {
		raw       string
		wantKey   string
		wantProto llmkit.Protocol
		wantOK    bool
	}{
		{"chat_completions", EndpointKeyChatCompletions, llmkit.ProtocolOpenAIChat, true},
		{"responses", EndpointKeyResponses, llmkit.ProtocolOpenAIResponses, true},
		{"messages", EndpointKeyMessages, llmkit.ProtocolClaudeMessages, true},
		{"anthropic", EndpointKeyMessages, llmkit.ProtocolClaudeMessages, true},
		{"chat-completion", EndpointKeyChatCompletions, llmkit.ProtocolOpenAIChat, true},
		{"", "", llmkit.ProtocolUnknown, false},
		{"embeddings", "", llmkit.ProtocolUnknown, false},
	}
	for _, c := range cases {
		k, p, ok := NormalizeEndpointType(c.raw)
		if k != c.wantKey || p != c.wantProto || ok != c.wantOK {
			t.Errorf("NormalizeEndpointType(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.raw, k, p, ok, c.wantKey, c.wantProto, c.wantOK)
		}
	}
}

func TestResolveTestEndpoint(t *testing.T) {
	const eps = `{"chat_completions":"/v1/chat/completions","messages":"/v1/messages"}`

	if k, p, path, err := ResolveTestEndpoint(eps, "", "anthropic"); err != nil ||
		k != EndpointKeyMessages || p != llmkit.ProtocolClaudeMessages || path != "/v1/messages" {
		t.Errorf("anthropic => (%q,%q,%q,%v)", k, p, path, err)
	}
	if _, _, path, err := ResolveTestEndpoint(eps, "", "responses"); err != nil || path != "/v1/responses" {
		t.Errorf("responses default path => (%q,%v)", path, err)
	}
	if k, _, path, err := ResolveTestEndpoint(eps, "", ""); err != nil ||
		k != EndpointKeyChatCompletions || path != "/v1/chat/completions" {
		t.Errorf("empty w/ endpoints => (%q,%q,%v)", k, path, err)
	}
	if k, _, path, err := ResolveTestEndpoint(`{"messages":"/v1/messages"}`, "", ""); err != nil ||
		k != EndpointKeyMessages || path != "/v1/messages" {
		t.Errorf("empty messages-only => (%q,%q,%v)", k, path, err)
	}
	if k, _, path, err := ResolveTestEndpoint("", "", ""); err != nil ||
		k != EndpointKeyChatCompletions || path != "/v1/chat/completions" {
		t.Errorf("empty no-endpoints => (%q,%q,%v)", k, path, err)
	}
	if _, _, _, err := ResolveTestEndpoint(eps, "", "embeddings"); err == nil {
		t.Error("embeddings should error")
	}
}

func TestPrimaryOutboundProtocol(t *testing.T) {
	if p := PrimaryOutboundProtocol("", ""); p != llmkit.ProtocolOpenAIChat {
		t.Fatalf("empty channel = %q, want openai_chat default", p)
	}
	if p := PrimaryOutboundProtocol(`{"messages":"/v1/messages"}`, ""); p != llmkit.ProtocolClaudeMessages {
		t.Fatalf("messages-only = %q, want claude", p)
	}
}

func TestBuildConnectivityTestRequest(t *testing.T) {
	path, body, err := BuildConnectivityTestRequest("", "", "anthropic", "claude-3", false)
	if err != nil || path != "/v1/messages" {
		t.Fatalf("anthropic path=%q err=%v", path, err)
	}
	if _, ok := body["messages"]; !ok {
		t.Errorf("messages body missing: %v", body)
	}
	_, body, err = BuildConnectivityTestRequest("", "", "responses", "gpt", true)
	if err != nil {
		t.Fatalf("responses err=%v", err)
	}
	if _, ok := body["input"]; !ok {
		t.Errorf("input body missing: %v", body)
	}
	if body["stream"] != true {
		t.Errorf("stream not set: %v", body)
	}
	if _, _, err := BuildConnectivityTestRequest("", "", "embeddings", "m", false); err == nil {
		t.Error("embeddings should error")
	}
}

// behavior change: relay connectivity tests must always enter ai-gateway
// through a standard inbound route; channel endpoints are outbound-only.
func TestBuildRelayConnectivityTestRequest(t *testing.T) {
	tests := []struct {
		name        string
		protocol    llmkit.Protocol
		stream      bool
		wantPath    string
		wantBodyKey string
		wantErr     bool
	}{
		{name: "chat", protocol: llmkit.ProtocolOpenAIChat, wantPath: "/v1/chat/completions", wantBodyKey: "messages"},
		{name: "responses streaming", protocol: llmkit.ProtocolOpenAIResponses, stream: true, wantPath: "/v1/responses", wantBodyKey: "input"},
		{name: "claude boundary", protocol: llmkit.ProtocolClaudeMessages, wantPath: "/v1/messages", wantBodyKey: "messages"},
		{name: "unknown protocol", protocol: llmkit.ProtocolUnknown, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, body, err := BuildRelayConnectivityTestRequest(tt.protocol, "test-model", tt.stream)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
			if body["model"] != "test-model" || body[tt.wantBodyKey] == nil {
				t.Errorf("body = %#v", body)
			}
			if got, exists := body["stream"]; exists != tt.stream || (exists && got != true) {
				t.Errorf("stream = %#v, exists=%v, want enabled=%v", got, exists, tt.stream)
			}
		})
	}
}

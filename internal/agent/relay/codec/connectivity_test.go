package codec

import "testing"

func TestNormalizeEndpointType(t *testing.T) {
	cases := []struct {
		raw      string
		wantKey  string
		wantProto Protocol
		wantOK   bool
	}{
		{"chat_completions", EndpointKeyChatCompletions, ProtocolOpenAIChat, true},
		{"responses", EndpointKeyResponses, ProtocolOpenAIResponses, true},
		{"messages", EndpointKeyMessages, ProtocolClaude, true},
		{"anthropic", EndpointKeyMessages, ProtocolClaude, true},
		{"chat-completion", EndpointKeyChatCompletions, ProtocolOpenAIChat, true},
		{"", "", ProtocolUnknown, false},
		{"embeddings", "", ProtocolUnknown, false},
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
		k != EndpointKeyMessages || p != ProtocolClaude || path != "/v1/messages" {
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
	if p := PrimaryOutboundProtocol("", ""); p != ProtocolOpenAIChat {
		t.Fatalf("empty channel = %q, want openai_chat default", p)
	}
	if p := PrimaryOutboundProtocol(`{"messages":"/v1/messages"}`, ""); p != ProtocolClaude {
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

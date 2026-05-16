package transform

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestSystemPromptInjector_NoopWhenEmpty(t *testing.T) {
	tr := SystemPromptInjector{}
	req := &codec.Request{Messages: []codec.Message{
		{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}},
	}}
	cfg := &codec.ChannelConfig{SystemPrompt: ""}

	tr.Transform(req, cfg)

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
}

func TestSystemPromptInjector_InjectsWhenSet(t *testing.T) {
	tr := SystemPromptInjector{}
	req := &codec.Request{Messages: []codec.Message{
		{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}},
	}}
	cfg := &codec.ChannelConfig{SystemPrompt: "you are a helpful assistant"}

	tr.Transform(req, cfg)

	if len(req.Messages) < 2 {
		t.Fatalf("expected >= 2 messages after injection, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != codec.RoleSystem {
		t.Fatalf("first message role = %q, want %q", req.Messages[0].Role, codec.RoleSystem)
	}
}

func TestSystemPromptInjector_AppliesToAllProtocols(t *testing.T) {
	tr := SystemPromptInjector{}
	for _, p := range []codec.Protocol{codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses, codec.ProtocolClaude} {
		if !tr.AppliesTo(p) {
			t.Fatalf("AppliesTo(%q) = false, want true", p)
		}
	}
}

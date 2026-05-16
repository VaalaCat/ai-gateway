package transform

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestStrip_NoopWhenEnabled(t *testing.T) {
	tr := ThinkingStripTransformer{}
	req := &codec.Request{Messages: []codec.Message{
		{
			Role: codec.RoleAssistant,
			Content: []codec.ContentBlock{
				{Type: codec.ContentTypeThinking, Text: "private reasoning"},
				{Type: codec.ContentTypeText, Text: "answer"},
			},
		},
	}}
	cfg := &codec.ChannelConfig{SendBackThinking: true}

	tr.Transform(req, cfg)

	if len(req.Messages[0].Content) != 2 {
		t.Fatalf("strip should noop when SendBackThinking=true; content len=%d", len(req.Messages[0].Content))
	}
}

func TestStrip_RemovesAllThinkingFromAssistant(t *testing.T) {
	tr := ThinkingStripTransformer{}
	req := &codec.Request{Messages: []codec.Message{
		{
			Role: codec.RoleAssistant,
			Content: []codec.ContentBlock{
				{Type: codec.ContentTypeThinking, Text: "t1"},
				{Type: codec.ContentTypeText, Text: "answer"},
				{Type: codec.ContentTypeThinking, Text: "t2"},
			},
		},
	}}
	cfg := &codec.ChannelConfig{SendBackThinking: false}

	tr.Transform(req, cfg)

	if got := len(req.Messages[0].Content); got != 1 {
		t.Fatalf("content blocks after strip = %d, want 1", got)
	}
	if req.Messages[0].Content[0].Type != codec.ContentTypeText {
		t.Fatalf("remaining block type = %q, want text", req.Messages[0].Content[0].Type)
	}
}

func TestStrip_DoesNotTouchOtherRoles(t *testing.T) {
	tr := ThinkingStripTransformer{}
	req := &codec.Request{Messages: []codec.Message{
		{
			Role: codec.RoleUser,
			Content: []codec.ContentBlock{
				{Type: codec.ContentTypeThinking, Text: "should stay"},
			},
		},
	}}
	cfg := &codec.ChannelConfig{SendBackThinking: false}

	tr.Transform(req, cfg)

	if len(req.Messages[0].Content) != 1 {
		t.Fatalf("user message thinking block was stripped")
	}
}

func TestStrip_OnlyAppliesToOpenAIChat(t *testing.T) {
	tr := ThinkingStripTransformer{}
	if !tr.AppliesTo(codec.ProtocolOpenAIChat) {
		t.Fatal("must apply to ProtocolOpenAIChat")
	}
	if tr.AppliesTo(codec.ProtocolClaude) {
		t.Fatal("must NOT apply to ProtocolClaude")
	}
	if tr.AppliesTo(codec.ProtocolOpenAIResponses) {
		t.Fatal("must NOT apply to ProtocolOpenAIResponses")
	}
}

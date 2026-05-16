package transform

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestPassthrough_NoopWhenDisabled(t *testing.T) {
	tr := ThinkingPassthroughTransformer{}
	req := &codec.Request{Messages: []codec.Message{
		{
			Role:      codec.RoleAssistant,
			ToolCalls: []codec.ToolCall{{ID: "c1", Name: "f", Arguments: "{}"}},
			Content:   []codec.ContentBlock{{Type: codec.ContentTypeText, Text: ""}},
		},
	}}
	cfg := &codec.ChannelConfig{SendBackThinking: false}

	tr.Transform(req, cfg)

	for _, b := range req.Messages[0].Content {
		if b.Type == codec.ContentTypeThinking {
			t.Fatal("passthrough must noop when SendBackThinking=false")
		}
	}
}

func TestPassthrough_NoopWhenWrongProtocol(t *testing.T) {
	tr := ThinkingPassthroughTransformer{}
	if tr.AppliesTo(codec.ProtocolClaude) {
		t.Fatal("must NOT apply to ProtocolClaude")
	}
	if tr.AppliesTo(codec.ProtocolOpenAIResponses) {
		t.Fatal("must NOT apply to ProtocolOpenAIResponses")
	}
	if !tr.AppliesTo(codec.ProtocolOpenAIChat) {
		t.Fatal("must apply to ProtocolOpenAIChat")
	}
}

func TestPassthrough_KeepsExistingThinking(t *testing.T) {
	tr := ThinkingPassthroughTransformer{}
	req := &codec.Request{Messages: []codec.Message{
		{
			Role:      codec.RoleAssistant,
			ToolCalls: []codec.ToolCall{{ID: "c1", Name: "f", Arguments: "{}"}},
			Content: []codec.ContentBlock{
				{Type: codec.ContentTypeThinking, Text: "kept"},
			},
		},
	}}
	cfg := &codec.ChannelConfig{SendBackThinking: true}

	tr.Transform(req, cfg)

	count := 0
	for _, b := range req.Messages[0].Content {
		if b.Type == codec.ContentTypeThinking {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("thinking blocks count = %d, want 1 (no duplicate)", count)
	}
}

func TestPassthrough_AddsPlaceholderForToolCallWithoutThinking(t *testing.T) {
	tr := ThinkingPassthroughTransformer{}
	req := &codec.Request{Messages: []codec.Message{
		{
			Role:      codec.RoleAssistant,
			ToolCalls: []codec.ToolCall{{ID: "c1", Name: "f", Arguments: "{}"}},
			Content:   []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "preamble"}},
		},
	}}
	cfg := &codec.ChannelConfig{SendBackThinking: true}

	tr.Transform(req, cfg)

	if len(req.Messages[0].Content) < 2 {
		t.Fatalf("expected at least 2 blocks after placeholder added, got %d", len(req.Messages[0].Content))
	}
	if req.Messages[0].Content[0].Type != codec.ContentTypeThinking {
		t.Fatalf("first block type = %q, want thinking", req.Messages[0].Content[0].Type)
	}
	if req.Messages[0].Content[0].Text != "" {
		t.Fatalf("placeholder text = %q, want empty", req.Messages[0].Content[0].Text)
	}
}

func TestPassthrough_PlainTextNoToolCallsNotPadded(t *testing.T) {
	tr := ThinkingPassthroughTransformer{}
	req := &codec.Request{Messages: []codec.Message{
		{
			Role:    codec.RoleAssistant,
			Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "answer"}},
		},
	}}
	cfg := &codec.ChannelConfig{SendBackThinking: true}

	tr.Transform(req, cfg)

	for _, b := range req.Messages[0].Content {
		if b.Type == codec.ContentTypeThinking {
			t.Fatal("plain assistant message must not get a thinking placeholder")
		}
	}
}

func TestPassthrough_Idempotent(t *testing.T) {
	tr := ThinkingPassthroughTransformer{}
	req := &codec.Request{Messages: []codec.Message{
		{
			Role:      codec.RoleAssistant,
			ToolCalls: []codec.ToolCall{{ID: "c1", Name: "f", Arguments: "{}"}},
			Content:   []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "preamble"}},
		},
	}}
	cfg := &codec.ChannelConfig{SendBackThinking: true}

	tr.Transform(req, cfg)
	first := len(req.Messages[0].Content)
	tr.Transform(req, cfg)
	second := len(req.Messages[0].Content)

	if first != second {
		t.Fatalf("non-idempotent: first=%d second=%d", first, second)
	}
}
